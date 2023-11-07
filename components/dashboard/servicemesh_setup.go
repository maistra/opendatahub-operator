package dashboard

import (
	"path"
	"path/filepath"

	operatorv1 "github.com/openshift/api/operator/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dsci "github.com/opendatahub-io/opendatahub-operator/v2/apis/dscinitialization/v1"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/deploy"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/feature"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/feature/servicemesh"
	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/gvr"
)

func (d *Dashboard) configureServiceMesh(cli client.Client, owner metav1.Object, dscispec *dsci.DSCInitializationSpec) error {
	shouldConfigureServiceMesh, err := deploy.ShouldConfigureServiceMesh(cli, dscispec)
	if err != nil {
		return err
	}

	if shouldConfigureServiceMesh {
		serviceMeshInitializer := feature.NewFeaturesInitializer(dscispec, d.defineServiceMeshFeatures(dscispec))

		if err := serviceMeshInitializer.Prepare(); err != nil {
			return err
		}

		if err := serviceMeshInitializer.Apply(); err != nil {
			return err
		}

		enabled := d.GetManagementState() == operatorv1.Managed
		if err := deploy.DeployManifestsFromPath(cli, owner, PathODHProjectController, dscispec.ApplicationsNamespace, ComponentName, enabled); err != nil {
			return err
		}
	}

	return nil
}

func (d *Dashboard) defineServiceMeshFeatures(dscispec *dsci.DSCInitializationSpec) feature.DefinedFeatures {
	return func(s *feature.FeaturesInitializer) error {
		var rootDir = filepath.Join(feature.BaseOutputDir, dscispec.ApplicationsNamespace)
		if err := feature.CopyEmbeddedFiles("templates", rootDir); err != nil {
			return err
		}

		createMeshResources, err := feature.CreateFeature("create-service-mesh-routing-resources-for-dashboard").
			For(dscispec).
			Manifests(
				path.Join(rootDir, feature.ControlPlaneDir, "components", d.GetComponentName()),
			).
			WithResources(servicemesh.EnabledInDashboard).
			WithData(servicemesh.ClusterDetails).
			PreConditions(
				feature.WaitForResourceToBeCreated(dscispec.ApplicationsNamespace, gvr.ODHDashboardConfigGVR),
			).
			PostConditions(
				feature.WaitForPodsToBeReady(dscispec.ServiceMesh.Mesh.Namespace),
			).
			OnDelete(servicemesh.DisabledInDashboard).
			Load()

		if err != nil {
			return err
		}

		s.Features = append(s.Features, createMeshResources)

		return nil
	}
}
