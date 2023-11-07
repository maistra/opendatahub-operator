package dscinitialization

import (
	operatorv1 "github.com/openshift/api/operator/v1"
	corev1 "k8s.io/api/core/v1"

	dsci "github.com/opendatahub-io/opendatahub-operator/v2/apis/dscinitialization/v1"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/feature"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/feature/serverless"
)

func (r *DSCInitializationReconciler) configureServerless(instance *dsci.DSCInitialization) error {
	if instance.Spec.Serverless.ManagementState == operatorv1.Managed {
		serverlessInitializer := feature.NewFeaturesInitializer(&instance.Spec, serverless.ConfigureServerlessFeatures)

		if err := serverlessInitializer.Prepare(); err != nil {
			r.Log.Error(err, "failed configuring serverless resources")
			r.Recorder.Eventf(instance, corev1.EventTypeWarning, "DSCInitializationReconcileError", "failed configuring serverless resources")

			return err
		}

		if err := serverlessInitializer.Apply(); err != nil {
			r.Log.Error(err, "failed applying serverless resources")
			r.Recorder.Eventf(instance, corev1.EventTypeWarning, "DSCInitializationReconcileError", "failed applying serverless resources")

			return err
		}
	}

	return nil
}
