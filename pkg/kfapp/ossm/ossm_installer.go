package ossm

import (
	"context"
	"embed"
	"fmt"
	"github.com/hashicorp/go-multierror"
	kfapisv3 "github.com/opendatahub-io/opendatahub-operator/apis"
	kftypesv3 "github.com/opendatahub-io/opendatahub-operator/apis/apps"
	"github.com/opendatahub-io/opendatahub-operator/pkg/kfconfig"
	"github.com/opendatahub-io/opendatahub-operator/pkg/kfconfig/ossmplugin"
	"github.com/pkg/errors"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"net/url"
	"path"
	"path/filepath"
	ctrlLog "sigs.k8s.io/controller-runtime/pkg/log"
	"strconv"
)

const (
	PluginName = "KfOssmPlugin"
)

// TODO rethink where it should belong
//
//go:embed templates
var embeddedFiles embed.FS

var log = ctrlLog.Log.WithName(PluginName)

type OssmInstaller struct {
	*kfconfig.KfConfig
	pluginSpec *ossmplugin.OssmPluginSpec
	config     *rest.Config
	features   []*Feature
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
	if err := o.KfConfig.GetPluginSpec(PluginName, o.pluginSpec); err != nil {
		return nil, err
	}

	// Populate target Kubeflow namespace to have it in one struct instead
	o.pluginSpec.AppNamespace = o.KfConfig.Namespace

	return o.pluginSpec, nil
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

	return o.enableFeatures()
}

func (o *OssmInstaller) enableFeatures() error {

	if err := o.SyncCache(); err != nil {
		return internalError(err)
	}

	var rootDir = filepath.Join(baseOutputDir, o.Namespace, o.Name)
	if copyFsErr := copyEmbeddedFS(embeddedFiles, "templates", rootDir); copyFsErr != nil {
		return internalError(errors.WithStack(copyFsErr))
	}

	if feature, err := EnableFeature("control-plane-oauth").
		For(o.pluginSpec).
		WithConfig(o.config).
		FromPaths(
			path.Join(rootDir, ControlPlaneDir, "base"),
			path.Join(rootDir, ControlPlaneDir, "oauth"),
			path.Join(rootDir, ControlPlaneDir, "filters"),
		).
		AdditionalResources(
			func(feature *Feature) error {
				if feature.spec.Mesh.Certificate.Generate {
					meta := metav1.ObjectMeta{
						Name:      feature.spec.Mesh.Certificate.Name,
						Namespace: feature.spec.Mesh.Namespace,
						OwnerReferences: []metav1.OwnerReference{
							feature.tracker.ToOwnerReference(),
						},
					}

					cert, err := generateSelfSignedCertificateAsSecret(feature.data.Domain, meta)
					if err != nil {
						return internalError(err)
					}

					if err != nil {
						return errors.WithStack(err)
					}

					_, err = feature.clientset.CoreV1().
						Secrets(feature.spec.Mesh.Namespace).
						Create(context.TODO(), cert, metav1.CreateOptions{})
					if err != nil && !k8serrors.IsAlreadyExists(err) {
						return errors.WithStack(err)
					}
				}

				return nil
			},
			func(feature *Feature) error {
				objectMeta := metav1.ObjectMeta{
					Name:      feature.spec.AppNamespace + "-oauth2-tokens",
					Namespace: feature.spec.Mesh.Namespace,
					OwnerReferences: []metav1.OwnerReference{
						feature.tracker.ToOwnerReference(),
					},
				}

				envoySecret, err := createEnvoySecret(feature.data.OAuth, objectMeta)
				if err != nil {
					return internalError(err)
				}

				_, err = feature.clientset.CoreV1().
					Secrets(objectMeta.Namespace).
					Create(context.TODO(), envoySecret, metav1.CreateOptions{})
				if err != nil && !k8serrors.IsAlreadyExists(err) {
					return errors.WithStack(err)
				}

				return nil
			},
		).
		WithData(loadData).
		Preconditions(
			checkIfCRDIsInstalled("operator.authorino.kuadrant.io", "v1beta1", "authorinos"),
			ensureServiceMeshInstalled,
		).
		OnDelete(
			removeOAuthClient,
			removeTokenVolumes,
		).Load(); err != nil {
		return nil
	} else {
		o.features = append(o.features, feature)
	}

	if feature, err := EnableFeature("shared-config-maps").
		For(o.pluginSpec).
		WithConfig(o.config).
		AdditionalResources(
			func(feature *Feature) error {
				if err := feature.createConfigMap("service-mesh-refs",
					map[string]string{
						"CONTROL_PLANE_NAME": feature.spec.Mesh.Name,
						"MESH_NAMESPACE":     feature.spec.Mesh.Namespace,
					}); err != nil {
					return internalError(err)
				}

				if err := feature.createConfigMap("auth-refs",
					map[string]string{
						"AUTHORINO_LABEL": feature.spec.Auth.Authorino.Label,
					}); err != nil {
					return internalError(err)
				}

				return nil
			},
		).Load(); err != nil {
		return err
	} else {
		o.features = append(o.features, feature)
	}

	if feature, err := EnableFeature("enable-service-mesh").
		For(o.pluginSpec).
		WithConfig(o.config).
		FromPaths(
			path.Join(rootDir, ControlPlaneDir, "smm.tmpl"),
			path.Join(rootDir, ControlPlaneDir, "namespace.patch.tmpl"),
		).
		WithData(loadData).
		Load(); err != nil {
		return err
	} else {
		o.features = append(o.features, feature)
	}

	if feature, err := EnableFeature("enable-service-mesh-for-dashboard").
		For(o.pluginSpec).
		WithConfig(o.config).
		AdditionalResources(
			func(feature *Feature) error {
				gvr := schema.GroupVersionResource{
					Group:    "opendatahub.io",
					Version:  "v1alpha",
					Resource: "odhdashboardconfigs",
				}

				configs, err := feature.dynamicClient.Resource(gvr).List(context.Background(), metav1.ListOptions{})
				if err != nil {
					return err
				}

				if len(configs.Items) == 0 {
					log.Info("No odhdashboardconfig found in namespace, doing nothing")
					return nil
				}

				// Assuming there is only one odhdashboardconfig in the namespace, patching the first one
				config := configs.Items[0]
				if config.Object["spec"] == nil {
					config.Object["spec"] = map[string]interface{}{}
				}
				spec := config.Object["spec"].(map[string]interface{})
				if spec["dashboardConfig"] == nil {
					spec["dashboardConfig"] = map[string]interface{}{}
				}
				dashboardConfig := spec["dashboardConfig"].(map[string]interface{})
				dashboardConfig["disableServiceMesh"] = false

				_, err = feature.dynamicClient.Resource(gvr).
					Namespace(feature.spec.AppNamespace).
					Update(context.Background(), &config, metav1.UpdateOptions{})
				if err != nil {
					log.Error(err, "Failed to update odhdashboardconfig")
					return err
				}

				log.Info("Successfully patched odhdashboardconfig")
				return nil

			},
		).Load(); err != nil {
		return err
	} else {
		o.features = append(o.features, feature)
	}

	if feature, err := EnableFeature("migrate-data-science-projects").
		For(o.pluginSpec).
		WithConfig(o.config).
		AdditionalResources(
			func(feature *Feature) error {
				selector := labels.SelectorFromSet(labels.Set{"opendatahub.io/dashboard": "true"})

				namespaceClient := feature.clientset.
					CoreV1().
					Namespaces()

				namespaces, err := namespaceClient.List(context.TODO(), metav1.ListOptions{LabelSelector: selector.String()})
				if err != nil {
					return fmt.Errorf("failed to get namespaces: %v", err)
				}

				var result *multierror.Error

				for _, namespace := range namespaces.Items {
					annotations := namespace.GetAnnotations()
					if annotations == nil {
						annotations = map[string]string{}
					}
					annotations["opendatahub.io/service-mesh"] = "true"
					namespace.SetAnnotations(annotations)

					if _, err := namespaceClient.Update(context.TODO(), &namespace, metav1.UpdateOptions{}); err != nil {
						result = multierror.Append(result, err)
					}
				}

				return result.ErrorOrNil()
			},
		).Load(); err != nil {
		return err
	} else {
		o.features = append(o.features, feature)
	}

	if feature, err := EnableFeature("setup-external-authorizaion").
		For(o.pluginSpec).
		WithConfig(o.config).
		FromPaths(
			path.Join(rootDir, AuthDir, "namespace.tmpl"),
			path.Join(rootDir, AuthDir, "auth-smm.tmpl"),
			path.Join(rootDir, AuthDir, "base"),
			path.Join(rootDir, AuthDir, "rbac"),
			path.Join(rootDir, AuthDir, "mesh-authz-ext-provider.patch.tmpl"),
		).
		WithData(loadData).
		OnDelete(
			func(feature *Feature) error {
				ossmAuthzProvider := fmt.Sprintf("%s-odh-auth-provider", feature.spec.AppNamespace)

				gvr := schema.GroupVersionResource{
					Group:    "maistra.io",
					Version:  "v2",
					Resource: "servicemeshcontrolplanes",
				}

				mesh := feature.spec.Mesh

				smcp, err := feature.dynamicClient.Resource(gvr).
					Namespace(mesh.Namespace).
					Get(context.Background(), mesh.Name, metav1.GetOptions{})
				if err != nil {
					return err
				}

				extensionProviders, found, err := unstructured.NestedSlice(smcp.Object, "spec", "techPreview", "meshConfig", "extensionProviders")
				if err != nil {
					return err
				}
				if !found {
					log.Info("no extension providers found", "smcp", mesh.Name, "istio-ns", mesh.Namespace)
					return nil
				}

				for i, v := range extensionProviders {
					extensionProvider, ok := v.(map[string]interface{})
					if !ok {
						fmt.Println("Unexpected type for extensionProvider")
						continue
					}

					if extensionProvider["name"] == ossmAuthzProvider {
						extensionProviders = append(extensionProviders[:i], extensionProviders[i+1:]...)
						err = unstructured.SetNestedSlice(smcp.Object, extensionProviders, "spec", "techPreview", "meshConfig", "extensionProviders")
						if err != nil {
							return err
						}
						break
					}
				}

				_, err = feature.dynamicClient.Resource(gvr).
					Namespace(mesh.Namespace).
					Update(context.Background(), smcp, metav1.UpdateOptions{})

				return err

			},
		).Load(); err != nil {
		return err
	} else {
		o.features = append(o.features, feature)
	}

	return nil
}

func (o *OssmInstaller) Generate(_ kftypesv3.ResourceEnum) error {
	var applyErrors *multierror.Error

	for _, feature := range o.features {
		err := feature.Apply()
		applyErrors = multierror.Append(applyErrors, err)
	}

	return applyErrors.ErrorOrNil()
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

func internalError(err error) error {
	return &kfapisv3.KfError{
		Code:    int(kfapisv3.INTERNAL_ERROR),
		Message: fmt.Sprintf("%+v", err),
	}
}
