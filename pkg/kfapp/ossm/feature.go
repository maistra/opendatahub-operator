package ossm

import (
	"context"
	"fmt"
	"github.com/hashicorp/go-multierror"
	"github.com/opendatahub-io/opendatahub-operator/apis/ossm.plugins.kubeflow.org/v1alpha1"
	"github.com/opendatahub-io/opendatahub-operator/pkg/kfconfig/ossmplugin"
	"github.com/opendatahub-io/opendatahub-operator/pkg/secret"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Feature struct {
	Name string

	clientset     *kubernetes.Clientset
	dynamicClient dynamic.Interface
	client        client.Client

	spec    *ossmplugin.OssmPluginSpec
	tracker *v1alpha1.OssmResourceTracker

	// TODO Rethink
	manifests      []manifest
	cleanups       []cleanup
	resources      []resourceCreator
	preconditions  []precondition
	postconditions []postcondition
	loader         dataLoader

	data *data
}

// TODO not sure if we need different signatures here
type postcondition func(feature *Feature) error
type resourceCreator func(feature *Feature) error
type cleanup func(feature *Feature) error
type precondition func(feature *Feature) error

type dataLoader func(feature *Feature) (*data, error)

func noopDataLoader(feature *Feature) (*data, error) {
	return nil, nil
}

func (f *Feature) Apply() error {
	// Verify all precondition and collect errors
	var multiErr *multierror.Error
	for _, precondition := range f.preconditions {
		multiErr = multierror.Append(multiErr, precondition(f))
	}

	if multiErr.ErrorOrNil() != nil {
		return multiErr.ErrorOrNil()
	}

	// Load necessary data
	var err error
	f.data, err = f.loader(f)
	if err != nil {
		return err
	}

	// Create resources
	for _, resource := range f.resources {
		if err := resource(f); err != nil {
			return err
		}
	}

	// Process and apply manifests
	for i, m := range f.manifests {
		if err := m.processTemplate(f.data); err != nil {
			return internalError(errors.WithStack(err))
		}

		fmt.Printf("%d: %+v\n", i, m)
	}

	if err := f.applyManifests(); err != nil {
		return err
	}

	// TODO postconditions

	return nil
}

func (f *Feature) Cleanup() error {

	var cleanupErrors *multierror.Error
	for _, cleanupFunc := range f.cleanups {
		cleanupErrors = multierror.Append(cleanupErrors, cleanupFunc(f))
	}

	return cleanupErrors.ErrorOrNil()
}

// TODO move to manifest itself? should it self-apply?
func (f *Feature) applyManifests() error {

	var apply func(filename string) error

	for _, m := range f.manifests {
		targetPath := m.targetPath()
		if m.patch {
			apply = func(filename string) error {
				log.Info("patching using manifest", "name", m.name, "path", targetPath)

				return f.patchResourceFromFile(filename)
			}
		} else {
			apply = func(filename string) error {
				log.Info("applying manifest", "name", m.name, "path", targetPath)

				return f.createResourceFromFile(filename)
			}
		}

		if err := apply(targetPath); err != nil {
			log.Error(err, "failed to create resource", "name", m.name, "path", targetPath)

			return err
		}
	}

	return nil
}

// FIXME not quite sure it belongs to a feature. Should be something "resource creator" facade instead
// including stuff below
// TMP to make stuff working
func (f *Feature) createConfigMap(cfgMapName string, data map[string]string) error {

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfgMapName,
			Namespace: f.spec.AppNamespace,
			OwnerReferences: []metav1.OwnerReference{
				f.tracker.ToOwnerReference(),
			},
		},
		Data: data,
	}

	configMaps := f.clientset.CoreV1().ConfigMaps(configMap.Namespace)

	_, err := configMaps.Get(context.TODO(), configMap.Name, metav1.GetOptions{})
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

// createResourceTracker instantiates OssmResourceTracker for given a Feature. All resources created when applying
// it will have this object attached as OwnerReference. It's a cluster-scoped resource.
// Once created, there's a cleanup hook added which will be invoked on deletion.
func (f *Feature) createResourceTracker() error {
	tracker := &v1alpha1.OssmResourceTracker{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "ossm.plugins.kubeflow.org/v1alpha1",
			Kind:       "OssmResourceTracker",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: f.spec.AppNamespace + "-" + f.Name,
		},
	}

	gvr := schema.GroupVersionResource{
		Group:    "ossm.plugins.kubeflow.org",
		Version:  "v1alpha1",
		Resource: "ossmresourcetrackers",
	}

	foundTracker, err := f.dynamicClient.Resource(gvr).Get(context.Background(), tracker.Name, metav1.GetOptions{})
	if k8serrors.IsNotFound(err) {
		unstructuredTracker, err := runtime.DefaultUnstructuredConverter.ToUnstructured(tracker)
		if err != nil {
			return err
		}

		u := unstructured.Unstructured{Object: unstructuredTracker}

		foundTracker, err = f.dynamicClient.Resource(gvr).Create(context.Background(), &u, metav1.CreateOptions{})
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	f.tracker = &v1alpha1.OssmResourceTracker{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(foundTracker.Object, f.tracker); err != nil {
		return err
	}

	// Register its own cleanup
	f.addCleanup(func(feature *Feature) error {
		if err := f.dynamicClient.Resource(gvr).Delete(context.Background(), f.tracker.Name, metav1.DeleteOptions{}); err != nil && !k8serrors.IsNotFound(err) {
			return err
		}

		return nil
	})

	return nil
}

func (f *Feature) addCleanup(cleanupFuncs ...cleanup) {
	f.cleanups = append(f.cleanups, cleanupFuncs...)
}

type data struct {
	*ossmplugin.OssmPluginSpec
	OAuth oAuth
	Domain,
	AppNamespace string
}

func loadData(feature *Feature) (*data, error) {
	data := &data{
		AppNamespace: feature.spec.AppNamespace,
	}

	data.OssmPluginSpec = feature.spec

	if domain, err := GetDomain(feature.dynamicClient); err == nil {
		data.Domain = domain
	} else {
		return nil, internalError(errors.WithStack(err))
	}

	var err error

	var clientSecret, hmac *secret.Secret
	if clientSecret, err = secret.NewSecret("ossm-odh-oauth", "random", 32); err != nil {
		return nil, internalError(errors.WithStack(err))
	}

	if hmac, err = secret.NewSecret("ossm-odh-hmac", "random", 32); err != nil {
		return nil, internalError(errors.WithStack(err))
	}

	if oauthServerDetailsJson, err := GetOAuthServerDetails(); err == nil {
		hostName, port, errUrlParsing := ExtractHostNameAndPort(oauthServerDetailsJson.Get("issuer").MustString("issuer"))
		if errUrlParsing != nil {
			return nil, internalError(errUrlParsing)
		}

		data.OAuth = oAuth{
			AuthzEndpoint: oauthServerDetailsJson.Get("authorization_endpoint").MustString("authorization_endpoint"),
			TokenEndpoint: oauthServerDetailsJson.Get("token_endpoint").MustString("token_endpoint"),
			Route:         hostName,
			Port:          port,
			ClientSecret:  clientSecret.Value,
			Hmac:          hmac.Value,
		}
	} else {
		return nil, internalError(errors.WithStack(err))
	}

	return data, nil
}
