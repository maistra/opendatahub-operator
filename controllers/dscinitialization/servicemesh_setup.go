package dscinitialization

import (
	operatorv1 "github.com/openshift/api/operator/v1"
	corev1 "k8s.io/api/core/v1"

	dsci "github.com/opendatahub-io/opendatahub-operator/v2/apis/dscinitialization/v1"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/deploy"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/feature"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/feature/servicemesh"
)

func (r *DSCInitializationReconciler) configureServiceMesh(instance *dsci.DSCInitialization) error {
	if instance.Spec.ServiceMesh.ManagementState == operatorv1.Managed {
		serviceMeshInitializer := feature.NewFeaturesInitializer(&instance.Spec, servicemesh.ConfigureServiceMeshFeatures)

		if err := serviceMeshInitializer.Prepare(); err != nil {
			r.Log.Error(err, "failed configuring service mesh resources")
			r.Recorder.Eventf(instance, corev1.EventTypeWarning, "DSCInitializationReconcileError", "failed configuring service mesh resources")

			return err
		}

		if err := serviceMeshInitializer.Apply(); err != nil {
			r.Log.Error(err, "failed applying service mesh resources")
			r.Recorder.Eventf(instance, corev1.EventTypeWarning, "DSCInitializationReconcileError", "failed applying service mesh resources")

			return err
		}
	}

	return nil
}

func (r *DSCInitializationReconciler) cleanupServiceMesh(instance *dsci.DSCInitialization) error {
	shouldConfigureServiceMesh, err := deploy.ShouldConfigureServiceMesh(r.Client, &instance.Spec)
	if err != nil {
		return err
	}

	if shouldConfigureServiceMesh {
		serviceMeshInitializer := feature.NewFeaturesInitializer(&instance.Spec, servicemesh.ConfigureServiceMeshFeatures)
		if err := serviceMeshInitializer.Prepare(); err != nil {
			return err
		}
		if err := serviceMeshInitializer.Delete(); err != nil {
			return err
		}
	}

	return nil
}
