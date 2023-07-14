package ossm

import (
	"context"
	"github.com/hashicorp/go-multierror"
	"github.com/opendatahub-io/opendatahub-operator/apis/ossm.plugins.kubeflow.org/v1alpha1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

type cleanup func() error

func (o *OssmInstaller) CleanupOwnedResources() error {
	var cleanupErrors *multierror.Error
	for _, cleanupFunc := range o.cleanupFuncs {
		cleanupErrors = multierror.Append(cleanupErrors, cleanupFunc())
	}

	return cleanupErrors.ErrorOrNil()
}

func (o *OssmInstaller) registerCleanup(cleanupFunc cleanup) {
	o.cleanupFuncs = append(o.cleanupFuncs, cleanupFunc)
}

// createResourceTracker instantiates OssmResourceTracker for given KfDef application in a namespce.
// This cluster-scoped resource is used as OwnerReference in all objects OssmInstaller is created across the cluster.
// Once created, there's a cleanup function added which will be invoked on deletion of the KfDef.
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

	o.registerCleanup(func() error {
		err := c.Resource(gvr).Delete(context.Background(), o.tracker.Name, metav1.DeleteOptions{})
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return err
	})

	return nil
}
