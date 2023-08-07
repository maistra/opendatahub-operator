package ossm

import (
	"context"
	"fmt"
	"github.com/hashicorp/go-multierror"
	kfapisv3 "github.com/opendatahub-io/opendatahub-operator/apis"
	kftypesv3 "github.com/opendatahub-io/opendatahub-operator/apis/apps"
	"github.com/opendatahub-io/opendatahub-operator/apis/ossm.plugins.kubeflow.org/v1alpha1"
	"github.com/opendatahub-io/opendatahub-operator/pkg/kfconfig"
	"github.com/opendatahub-io/opendatahub-operator/pkg/kfconfig/ossmplugin"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"net/url"
	ctrlLog "sigs.k8s.io/controller-runtime/pkg/log"
	"strconv"
)

const (
	PluginName = "KfOssmPlugin"
)

var log = ctrlLog.Log.WithName(PluginName)

type OssmInstaller struct {
	*kfconfig.KfConfig
	pluginSpec   *ossmplugin.OssmPluginSpec
	config       *rest.Config
	manifests    []manifest
	tracker      *v1alpha1.OssmResourceTracker
	cleanupFuncs []cleanup
}

func NewOssmInstaller(kfConfig *kfconfig.KfConfig, restConfig *rest.Config) *OssmInstaller {
	return &OssmInstaller{
		KfConfig: kfConfig,
		config:   restConfig,
	}

}

// GetPlatform returns the ossm kfapp. It's called by coordinator.GetPlatform
func GetPlatform(kfConfig *kfconfig.KfConfig) (kftypesv3.Platform, error) {
	return NewOssmInstaller(kfConfig, kftypesv3.GetConfig()), nil
}

// GetPluginSpec gets the plugin spec.
func (o *OssmInstaller) GetPluginSpec() (*ossmplugin.OssmPluginSpec, error) {
	if o.pluginSpec != nil {
		return o.pluginSpec, nil
	}

	o.pluginSpec = &ossmplugin.OssmPluginSpec{}
	err := o.KfConfig.GetPluginSpec(PluginName, o.pluginSpec)

	return o.pluginSpec, err
}

func (o *OssmInstaller) Init(_ kftypesv3.ResourceEnum) error {
	if o.KfConfig.Spec.SkipInitProject {
		log.Info("Skipping init phase", "plugin", PluginName)
	}

	log.Info("Initializing", "plugin", PluginName)
	pluginSpec, err := o.GetPluginSpec()
	if err != nil {
		return internalError(errors.WithStack(err))
	}

	pluginSpec.SetDefaults()

	if valid, reason := pluginSpec.IsValid(); !valid {
		return internalError(errors.New(reason))
	}

	if err := o.CheckForCRD("operator.authorino.kuadrant.io", "v1beta1", "authorinos"); err != nil {
		log.Info("Failed to find the pre-requisite authorinos CRD, please ensure Authorino operator is installed.")
		return internalError(err)
	}
	if err := o.CheckForCRD("maistra.io", "v2", "servicemeshcontrolplanes"); err != nil {
		log.Info("Failed to find the pre-requisite SMCP CRD, please ensure OSSM operator is installed.")
		return internalError(err)
	}
	status, err := o.CheckSMCPStatus(pluginSpec.Mesh.Name, pluginSpec.Mesh.Namespace)
	if err != nil {
		log.Info("An error occurred while checking SMCP status - ensure the SMCP referenced exists.")
		return internalError(err)
	}
	if status != "Ready" {
		log.Info("The referenced SMCP is not ready.")
		return internalError(errors.New("SMCP status is not ready"))
	}

	if err := o.createResourceTracker(); err != nil {
		return internalError(err)
	}

	if err := o.createConfigMap("service-mesh-refs",
		map[string]string{
			"CONTROL_PLANE_NAME": pluginSpec.Mesh.Name,
			"MESH_NAMESPACE":     pluginSpec.Mesh.Namespace,
		}); err != nil {
		return internalError(err)
	}

	if err := o.createConfigMap("auth-refs",
		map[string]string{
			"AUTHORINO_LABEL": pluginSpec.Auth.Authorino.Label,
		}); err != nil {
		return internalError(err)
	}

	if err := o.MigrateDSProjects(); err != nil {
		log.Error(err, "failed migrating Data Science Projects")
	}

	if err := o.processManifests(); err != nil {
		return internalError(err)
	}

	return nil
}

func (o *OssmInstaller) Generate(resources kftypesv3.ResourceEnum) error {
	// TODO sort by Kind as .Apply does
	if err := o.applyManifests(); err != nil {
		return internalError(errors.WithStack(err))
	}

	o.onCleanup(
		o.oauthClientRemoval(),
		o.ingressVolumesRemoval(),
		o.externalAuthzProviderRemoval(),
	)

	return nil
}

// ExtractHostNameAndPort strips given URL in string from http(s):// prefix and subsequent path,
// returning host name and port if defined (otherwise defaults to 443).
//
// This is useful when getting value from http headers (such as origin).
// If given string does not start with http(s) prefix it will be returned as is.
func ExtractHostNameAndPort(s string) (string, string, error) {
	u, err := url.Parse(s)
	if err != nil {
		return "", "", err
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return s, "", nil
	}

	hostname := u.Hostname()

	port := "443" // default for https
	if u.Scheme == "http" {
		port = "80"
	}

	if u.Port() != "" {
		port = u.Port()
		_, err := strconv.Atoi(port)
		if err != nil {
			return "", "", errors.New("invalid port number: " + port)
		}
	}

	return hostname, port, nil
}

func (o *OssmInstaller) createConfigMap(cfgMapName string, data map[string]string) error {

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfgMapName,
			Namespace: o.KfConfig.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: o.tracker.APIVersion,
					Kind:       o.tracker.Kind,
					Name:       o.tracker.Name,
					UID:        o.tracker.UID,
				},
			},
		},
		Data: data,
	}

	client, err := clientset.NewForConfig(o.config)
	if err != nil {
		return err
	}

	configMaps := client.CoreV1().ConfigMaps(configMap.Namespace)
	_, err = configMaps.Get(context.TODO(), configMap.Name, metav1.GetOptions{})
	if k8serrors.IsNotFound(err) {
		_, err = configMaps.Create(context.TODO(), configMap, metav1.CreateOptions{})
		if err != nil {
			return err
		}

	} else if k8serrors.IsAlreadyExists(err) {
		_, err = configMaps.Update(context.TODO(), configMap, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	} else {
		return err
	}

	return nil
}

func (o *OssmInstaller) MigrateDSProjects() error {

	client, err := clientset.NewForConfig(o.config)
	if err != nil {
		return err
	}

	selector := labels.SelectorFromSet(labels.Set{"opendatahub.io/dashboard": "true"})

	namespaces, err := client.CoreV1().Namespaces().List(context.TODO(), metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return fmt.Errorf("failed to get namespaces: %v", err)
	}

	var result *multierror.Error

	for _, ns := range namespaces.Items {
		annotations := ns.GetAnnotations()
		if annotations == nil {
			annotations = map[string]string{}
		}
		annotations["opendatahub.io/service-mesh"] = "true"
		ns.SetAnnotations(annotations)

		if _, err := client.CoreV1().Namespaces().Update(context.TODO(), &ns, metav1.UpdateOptions{}); err != nil {
			result = multierror.Append(result, err)
		}
	}

	return result.ErrorOrNil()
}

func (o *OssmInstaller) checkOperatorsExist(operatorNames []string) (bool, error) {
	client, err := clientset.NewForConfig(o.config)
	deploymentsClient := client.AppsV1().Deployments("") // empty string for namespace lists across all namespaces
	if err != nil {
		return false, err
	}

	// create map for lookup speed
	operatorNamesMap := make(map[string]bool)
	for _, name := range operatorNames {
		operatorNamesMap[name] = false
	}

	deployments, err := deploymentsClient.List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return false, errors.Wrap(err, "Failed to get deployments")
	}
	for _, dep := range deployments.Items {
		if _, found := operatorNamesMap[dep.Name]; found {
			operatorNamesMap[dep.Name] = true
		}
	}

	for _, isFound := range operatorNamesMap {
		if !isFound {
			return false, nil
		}
	}

	return true, nil
}

func internalError(err error) error {
	return &kfapisv3.KfError{
		Code:    int(kfapisv3.INTERNAL_ERROR),
		Message: fmt.Sprintf("%+v", err),
	}
}
