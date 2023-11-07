package servicemesh

import (
	"path"
	"path/filepath"

	"github.com/opendatahub-io/opendatahub-operator/v2/pkg/feature"
)

const templatesDir = "templates/servicemesh"

func ConfigureServiceMeshFeatures(f *feature.FeaturesInitializer) error {
	var rootDir = filepath.Join(feature.BaseOutputDir, f.DSCInitializationSpec.ApplicationsNamespace)
	if err := feature.CopyEmbeddedFiles(templatesDir, rootDir); err != nil {
		return err
	}

	serviceMeshSpec := f.ServiceMesh

	smcpCreation, errSmcp := feature.CreateFeature("mesh-control-plane-creation").
		For(f.DSCInitializationSpec).
		Manifests(
			// TODO align
			path.Join(rootDir, templatesDir, "base"),
		).
		PreConditions(
			EnsureServiceMeshOperatorInstalled,
			feature.CreateNamespace(serviceMeshSpec.Mesh.Namespace),
		).
		PostConditions(
			feature.WaitForPodsToBeReady(serviceMeshSpec.Mesh.Namespace),
		).
		Load()
	if errSmcp != nil {
		return errSmcp
	}
	f.Features = append(f.Features, smcpCreation)

	if serviceMeshSpec.Mesh.MetricsCollection == "Istio" {
		metricsCollection, errMetrics := feature.CreateFeature("mesh-metrics-collection").
			For(f.DSCInitializationSpec).
			Manifests(
				// TODO align
				path.Join(rootDir, templatesDir, "metrics-collection"),
			).
			PreConditions(
				EnsureServiceMeshInstalled,
			).
			Load()
		if errMetrics != nil {
			return errMetrics
		}
		f.Features = append(f.Features, metricsCollection)
	}

	if oauth, err := feature.CreateFeature("control-plane-configure-oauth").
		For(f.DSCInitializationSpec).
		Manifests(
			path.Join(rootDir, feature.ControlPlaneDir, "base"),
			path.Join(rootDir, feature.ControlPlaneDir, "oauth"),
			path.Join(rootDir, feature.ControlPlaneDir, "filters"),
		).
		WithResources(
			func(f *feature.Feature) error {
				return f.CreateSelfSignedCertificate(f.Spec.Mesh.Certificate, f.Spec.Domain, f.Spec.Mesh.Namespace)
			},
			EnvoyOAuthSecrets,
		).
		WithData(ClusterDetails, OAuthConfig).
		PreConditions(EnsureServiceMeshInstalled).
		PostConditions(
			feature.WaitForPodsToBeReady(serviceMeshSpec.Mesh.Namespace),
		).
		OnDelete(
			RemoveOAuthClient,
			RemoveTokenVolumes,
		).Load(); err != nil {
		return err
	} else {
		f.Features = append(f.Features, oauth)
	}

	if cfMaps, err := feature.CreateFeature("shared-config-maps").
		For(f.DSCInitializationSpec).
		WithResources(ConfigMaps).
		Load(); err != nil {
		return err
	} else {
		f.Features = append(f.Features, cfMaps)
	}

	if serviceMesh, err := feature.CreateFeature("app-add-namespace-to-service-mesh").
		For(f.DSCInitializationSpec).
		Manifests(
			path.Join(rootDir, feature.ControlPlaneDir, "smm.tmpl"),
			path.Join(rootDir, feature.ControlPlaneDir, "namespace.patch.tmpl"),
		).
		WithData(ClusterDetails).
		Load(); err != nil {
		return err
	} else {
		f.Features = append(f.Features, serviceMesh)
	}

	if gatewayRoute, err := feature.CreateFeature("create-gateway-route").
		For(f.DSCInitializationSpec).
		Manifests(
			path.Join(rootDir, feature.ControlPlaneDir, "routing"),
		).
		WithData(ClusterDetails).
		PostConditions(
			feature.WaitForPodsToBeReady(serviceMeshSpec.Mesh.Namespace),
		).
		Load(); err != nil {
		return err
	} else {
		f.Features = append(f.Features, gatewayRoute)
	}

	if dataScienceProjects, err := feature.CreateFeature("app-migrate-data-science-projects").
		For(f.DSCInitializationSpec).
		WithResources(MigratedDataScienceProjects).
		Load(); err != nil {
		return err
	} else {
		f.Features = append(f.Features, dataScienceProjects)
	}

	if extAuthz, err := feature.CreateFeature("control-plane-setup-external-authorization").
		For(f.DSCInitializationSpec).
		Manifests(
			path.Join(rootDir, feature.AuthDir, "auth-smm.tmpl"),
			path.Join(rootDir, feature.AuthDir, "base"),
			path.Join(rootDir, feature.AuthDir, "rbac"),
			path.Join(rootDir, feature.AuthDir, "mesh-authz-ext-provider.patch.tmpl"),
		).
		WithData(ClusterDetails).
		PreConditions(
			feature.CreateNamespace(serviceMeshSpec.Auth.Namespace),
			feature.EnsureCRDIsInstalled("authconfigs.authorino.kuadrant.io"),
			EnsureServiceMeshInstalled,
		).
		PostConditions(
			feature.WaitForPodsToBeReady(serviceMeshSpec.Mesh.Namespace),
			feature.WaitForPodsToBeReady(serviceMeshSpec.Auth.Namespace),
		).
		OnDelete(RemoveExtensionProvider).
		Load(); err != nil {
		return err
	} else {
		f.Features = append(f.Features, extAuthz)
	}

	return nil
}
