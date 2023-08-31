package feature

import (
	"context"
	"github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"
)

func EnsureCRDIsInstalled(name string) action {
	return func(f *Feature) error {
		return f.client.Get(context.Background(), client.ObjectKey{Name: name}, &apiextv1.CustomResourceDefinition{})
	}
}

func EnsureServiceMeshInstalled(feature *Feature) error {
	if err := EnsureCRDIsInstalled("servicemeshcontrolplanes.maistra.io")(feature); err != nil {
		log.Info("Failed to find the pre-requisite Service Mesh Control Plane CRD, please ensure Service Mesh Operator is installed.")

		return err
	}

	smcp := feature.Spec.Mesh.Name
	smcpNs := feature.Spec.Mesh.Namespace

	ready, err := CheckControlPlaneComponentReadiness(feature.dynamicClient, smcp, smcpNs)
	if err != nil || !ready {
		log.Error(err, "failed waiting for control plane being ready", "name", smcp, "namespace", smcpNs)

		return multierror.Append(err, errors.New("service mesh control plane is not ready")).ErrorOrNil()
	}

	return nil
}

const (
	interval = 1 * time.Second
	duration = 5 * time.Minute
)

func WaitForControlPlaneToBeReady(feature *Feature) error {
	return wait.PollImmediate(interval, duration, func() (done bool, err error) {
		smcp := feature.Spec.Mesh.Name
		smcpNs := feature.Spec.Mesh.Namespace

		log.Info("waiting for control plane components to be ready", "name", smcp, "namespace", smcpNs, "duration (s)", duration.Seconds())

		return CheckControlPlaneComponentReadiness(feature.dynamicClient, smcp, smcpNs)
	})
}

func CheckControlPlaneComponentReadiness(dynamicClient dynamic.Interface, smcp, smcpNs string) (bool, error) {
	unstructObj, err := dynamicClient.Resource(smcpGVR).Namespace(smcpNs).Get(context.Background(), smcp, metav1.GetOptions{})
	if err != nil {
		log.Info("failed to find Service Mesh Control Plane", "name", smcp, "namespace", smcpNs)

		return false, err
	}

	components, found, err := unstructured.NestedMap(unstructObj.Object, "status", "readiness", "components")
	if err != nil || !found {
		log.Info("status conditions not found or error in parsing of Service Mesh Control Plane")

		return false, err
	}

	readyComponents := len(components["ready"].([]interface{}))
	pendingComponents := len(components["pending"].([]interface{}))
	unreadyComponents := len(components["unready"].([]interface{}))

	return pendingComponents == 0 && unreadyComponents == 0 && readyComponents > 0, nil
}
