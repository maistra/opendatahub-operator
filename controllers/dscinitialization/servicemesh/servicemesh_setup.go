package servicemesh

import (
	"github.com/hashicorp/go-multierror"
	v1 "github.com/opendatahub-io/opendatahub-operator/v2/apis/dscinitialization/v1"
	"github.com/opendatahub-io/opendatahub-operator/v2/controllers/dscinitialization/servicemesh/feature"
	"github.com/pkg/errors"
	"path"
	"path/filepath"
	ctrlLog "sigs.k8s.io/controller-runtime/pkg/log"
)

var log = ctrlLog.Log.WithName("service-mesh")

type ServiceMeshInitializer struct {
	*v1.DSCInitializationSpec
	features []*feature.Feature
}

func NewServiceMeshInitializer(spec *v1.DSCInitializationSpec) (*ServiceMeshInitializer, error) {
	return &ServiceMeshInitializer{
		DSCInitializationSpec: spec,
	}, nil
}

// Configure performs validation of the spec and ensures all resources,
// such as features and their templates, are processed and initialized
// before proceeding with the actual cluster set-up.
func (s *ServiceMeshInitializer) Configure() error {
	log.Info("Initializing Service Mesh configuration")

	serviceMeshSpec := &s.DSCInitializationSpec.ServiceMesh

	if err := serviceMeshSpec.SetDefaults(); err != nil {
		return errors.WithStack(err)
	}

	if valid, reason := serviceMeshSpec.IsValid(); !valid {
		return errors.New(reason)
	}

	// TODO rework how this should be handled (reverse control - component calls it instead?)
	/*
		if err := s.addServiceMeshOverlays(); err != nil {
			return internalError(err)
		}

		if err := s.addOssmEnvFile("USE_ISTIO", "true", "ISTIO_GATEWAY", fmt.Sprintf("%s/%s", pluginSpec.AppNamespace, "odh-gateway")); err != nil {
			return errors.WithStack(err)
		}
	*/

	if err := s.configureServiceMeshFeatures(); err != nil {
		return err
	}

	var applyErrors *multierror.Error

	for _, f := range s.features {
		err := f.Apply()
		applyErrors = multierror.Append(applyErrors, err)
	}

	return applyErrors.ErrorOrNil()
}

// Delete ... TODO to be called as part of finalizer
func (s *ServiceMeshInitializer) Delete() error {
	var cleanupErrors *multierror.Error
	// Performs cleanups in reverse order (stack), this way e.g. we can unpatch SMCP before deleting it (if managed)
	// Though it sounds unnecessary it keeps features isolated and there is no need to rely on the InstallationMode
	// between the features when it comes to clean-up. This is based on the assumption, that features
	// are created in the correct order or are self-contained.
	for i := len(s.features) - 1; i >= 0; i-- {
		log.Info("cleanup", "name", s.features[i].Name)
		cleanupErrors = multierror.Append(cleanupErrors, s.features[i].Cleanup())
	}

	return cleanupErrors.ErrorOrNil()
}

func (s *ServiceMeshInitializer) configureServiceMeshFeatures() error {

	var rootDir = filepath.Join(feature.BaseOutputDir, s.DSCInitializationSpec.ApplicationsNamespace)
	if err := CopyEmbeddedFiles("templates", rootDir); err != nil {
		return errors.WithStack(err)
	}

	serviceMeshSpec := s.ServiceMesh

	if oauth, err := feature.CreateFeature("control-plane-configure-oauth").
		For(s.DSCInitializationSpec).
		Manifests(
			path.Join(rootDir, feature.ControlPlaneDir, "base"),
			path.Join(rootDir, feature.ControlPlaneDir, "oauth"),
			path.Join(rootDir, feature.ControlPlaneDir, "filters"),
		).
		WithResources(
			feature.SelfSignedCertificate,
			feature.EnvoyOAuthSecrets,
		).
		WithData(feature.ClusterDetails, feature.OAuthConfig).
		Preconditions(
			feature.EnsureServiceMeshInstalled,
		).
		Postconditions(
			feature.WaitForPodsToBeReady(serviceMeshSpec.Mesh.Namespace),
		).
		OnDelete(
			feature.RemoveOAuthClient,
			feature.RemoveTokenVolumes,
		).Load(); err != nil {
		return err
	} else {
		s.features = append(s.features, oauth)
	}

	if cfMaps, err := feature.CreateFeature("shared-config-maps").
		For(s.DSCInitializationSpec).
		WithResources(feature.ConfigMaps).
		Load(); err != nil {
		return err
	} else {
		s.features = append(s.features, cfMaps)
	}

	if serviceMesh, err := feature.CreateFeature("app-add-namespace-to-service-mesh").
		For(s.DSCInitializationSpec).
		Manifests(
			path.Join(rootDir, feature.ControlPlaneDir, "smm.tmpl"),
			path.Join(rootDir, feature.ControlPlaneDir, "namespace.patch.tmpl"),
		).
		WithData(feature.ClusterDetails).
		Load(); err != nil {
		return err
	} else {
		s.features = append(s.features, serviceMesh)
	}

	if gatewayRoute, err := feature.CreateFeature("create-gateway-route").
		For(s.DSCInitializationSpec).
		Manifests(
			path.Join(rootDir, feature.ControlPlaneDir, "routing"),
		).
		WithResources(feature.ServiceMeshEnabledInDashboard).
		WithData(feature.ClusterDetails).
		Postconditions(
			feature.WaitForPodsToBeReady(serviceMeshSpec.Mesh.Namespace),
		).
		Load(); err != nil {
		return err
	} else {
		s.features = append(s.features, gatewayRoute)
	}

	if dataScienceProjects, err := feature.CreateFeature("app-migrate-data-science-projects").
		For(s.DSCInitializationSpec).
		WithResources(feature.MigratedDataScienceProjects).
		Load(); err != nil {
		return err
	} else {
		s.features = append(s.features, dataScienceProjects)
	}

	if extAuthz, err := feature.CreateFeature("control-plane-setup-external-authorization").
		For(s.DSCInitializationSpec).
		Manifests(
			path.Join(rootDir, feature.AuthDir, "auth-smm.tmpl"),
			path.Join(rootDir, feature.AuthDir, "base"),
			path.Join(rootDir, feature.AuthDir, "rbac"),
			path.Join(rootDir, feature.AuthDir, "mesh-authz-ext-provider.patch.tmpl"),
		).
		WithData(feature.ClusterDetails).
		Preconditions(
			feature.CreateNamespace(serviceMeshSpec.Auth.Namespace),
			feature.EnsureCRDIsInstalled("authconfigs.authorino.kuadrant.io"),
			feature.EnsureServiceMeshInstalled,
		).
		Postconditions(
			feature.WaitForPodsToBeReady(serviceMeshSpec.Mesh.Namespace),
			feature.WaitForPodsToBeReady(serviceMeshSpec.Auth.Namespace),
		).
		OnDelete(feature.RemoveExtensionProvider).
		Load(); err != nil {
		return err
	} else {
		s.features = append(s.features, extAuthz)
	}

	return nil
}
