package ossm

import (
	"context"
	"fmt"
	multierror "github.com/hashicorp/go-multierror"
	kfapisv3 "github.com/opendatahub-io/opendatahub-operator/apis"
	kftypesv3 "github.com/opendatahub-io/opendatahub-operator/apis/apps"
	"github.com/opendatahub-io/opendatahub-operator/apis/ossm.plugins.kubeflow.org/v1alpha1"
	"github.com/opendatahub-io/opendatahub-operator/pkg/kfconfig"
	"github.com/opendatahub-io/opendatahub-operator/pkg/kfconfig/ossmplugin"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"regexp"
	ctrlLog "sigs.k8s.io/controller-runtime/pkg/log"
	"strings"
)

const (
	PluginName = "KfOssmPlugin"
)

var log = ctrlLog.Log.WithName(PluginName)

type cleanup func() error

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
		log.Info("Skipping init phase")
	}

	log.Info("Initializing " + PluginName)
	pluginSpec, err := o.GetPluginSpec()
	if err != nil {
		return internalError(errors.WithStack(err))
	}

	pluginSpec.SetDefaults()

	if valid, reason := pluginSpec.IsValid(); !valid {
		return internalError(errors.New(reason))
	}

	// TODO ensure operators are installed

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

	o.cleanupFuncs = append(o.cleanupFuncs, func() error {
		c, err := dynamic.NewForConfig(o.config)
		if err != nil {
			return err
		}

		oauthClientName := fmt.Sprintf("%s-oauth2-client", o.KfConfig.Namespace)
		gvr := schema.GroupVersionResource{
			Group:    "oauth.openshift.io",
			Version:  "v1",
			Resource: "oauthclients",
		}

		err = c.Resource(gvr).Delete(context.Background(), oauthClientName, metav1.DeleteOptions{})
		if k8serrors.IsNotFound(err) {
			return nil
		}

		log.Error(err, "failed deleting OAuthClient", "name", oauthClientName)
		return err
	})

	return nil
}

func (o *OssmInstaller) createResourceTracker() error {
	tracker := &v1alpha1.OssmResourceTracker{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "ossm.plugins.kubeflow.org/v1alpha1",
			Kind:       "OssmResourceTracker",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: o.KfConfig.Name + "." + o.KfConfig.Namespace,
		},
	}

	c, err := dynamic.NewForConfig(o.config)
	if err != nil {
		return err
	}

	gvr := schema.GroupVersionResource{
		Group:    "ossm.plugins.kubeflow.org",
		Version:  "v1alpha1",
		Resource: "ossmresourcetrackers",
	}

	foundTracker, err := c.Resource(gvr).Get(context.Background(), tracker.Name, metav1.GetOptions{})
	if k8serrors.IsNotFound(err) {
		unstructuredTracker, err := runtime.DefaultUnstructuredConverter.ToUnstructured(tracker)
		if err != nil {
			return err
		}

		u := unstructured.Unstructured{Object: unstructuredTracker}

		foundTracker, err = c.Resource(gvr).Create(context.Background(), &u, metav1.CreateOptions{})
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	o.tracker = &v1alpha1.OssmResourceTracker{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(foundTracker.Object, o.tracker); err != nil {
		return err
	}

	o.cleanupFuncs = append(o.cleanupFuncs, func() error {
		err := c.Resource(gvr).Delete(context.Background(), o.tracker.Name, metav1.DeleteOptions{})
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	})

	return nil
}

func (o *OssmInstaller) Generate(resources kftypesv3.ResourceEnum) error {
	// TODO sort by Kind as .Apply does
	if err := o.applyManifests(); err != nil {
		return internalError(errors.WithStack(err))
	}

	return nil
}

// ExtractHostName strips given URL in string from http(s):// prefix and subsequent path.
// This is useful when getting value from http headers (such as origin).
// If given string does not start with http(s) prefix it will be returned as is.
func ExtractHostName(s string) string {
	r := regexp.MustCompile(`^(https?://)`)
	withoutProtocol := r.ReplaceAllString(s, "")
	if s == withoutProtocol {
		return s
	}
	index := strings.Index(withoutProtocol, "/")
	if index == -1 {
		return withoutProtocol
	}
	return withoutProtocol[:index]
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

func (o *OssmInstaller) CleanupOwnedResources() error {
	var cleanupErrors *multierror.Error
	for _, cleanupFunc := range o.cleanupFuncs {
		cleanupErrors = multierror.Append(cleanupErrors, cleanupFunc())
	}

	return cleanupErrors.ErrorOrNil()
}

func internalError(err error) error {
	return &kfapisv3.KfError{
		Code:    int(kfapisv3.INTERNAL_ERROR),
		Message: fmt.Sprintf("%+v", err),
	}
}
