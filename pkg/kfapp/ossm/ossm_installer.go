package ossm

import (
	"embed"
	"fmt"
	"github.com/hashicorp/go-multierror"
	kfapisv3 "github.com/opendatahub-io/opendatahub-operator/apis"
	kftypesv3 "github.com/opendatahub-io/opendatahub-operator/apis/apps"
	"github.com/opendatahub-io/opendatahub-operator/pkg/kfapp/ossm/feature"
	"github.com/opendatahub-io/opendatahub-operator/pkg/kfconfig"
	"github.com/opendatahub-io/opendatahub-operator/pkg/kfconfig/ossmplugin"
	"github.com/pkg/errors"
	"io/fs"
	"k8s.io/client-go/rest"
	"os"
	"path"
	"path/filepath"
	ctrlLog "sigs.k8s.io/controller-runtime/pkg/log"
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
	features   []*feature.Feature
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

	var rootDir = filepath.Join(feature.BaseOutputDir, o.Namespace, o.Name)
	if copyFsErr := copyEmbeddedFS(embeddedFiles, "templates", rootDir); copyFsErr != nil {
		return internalError(errors.WithStack(copyFsErr))
	}

	if oauth, err := feature.CreateFeature("control-plane-oauth").
		For(o.pluginSpec).
		WithConfig(o.config).
		FromPaths(
			path.Join(rootDir, feature.ControlPlaneDir, "base"),
			path.Join(rootDir, feature.ControlPlaneDir, "oauth"),
			path.Join(rootDir, feature.ControlPlaneDir, "filters"),
		).
		AdditionalResources(
			feature.GenerateSelfSignedCertificate,
			feature.GenerateEnvoySecrets,
		).
		WithData(feature.LoadClusterData).
		Preconditions(
			feature.EnsureCRDIsInstalled("operator.authorino.kuadrant.io", "v1beta1", "authorinos"),
			feature.EnsureServiceMeshInstalled,
		).
		OnDelete(
			feature.RemoveOAuthClient,
			feature.RemoveTokenVolumes,
		).Load(); err != nil {
		return nil
	} else {
		o.features = append(o.features, oauth)
	}

	if cfMaps, err := feature.CreateFeature("shared-config-maps").
		For(o.pluginSpec).
		WithConfig(o.config).
		AdditionalResources(feature.CreateConfigMaps).
		Load(); err != nil {
		return err
	} else {
		o.features = append(o.features, cfMaps)
	}

	if serviceMesh, err := feature.CreateFeature("enable-service-mesh").
		For(o.pluginSpec).
		WithConfig(o.config).
		FromPaths(
			path.Join(rootDir, feature.ControlPlaneDir, "smm.tmpl"),
			path.Join(rootDir, feature.ControlPlaneDir, "namespace.patch.tmpl"),
		).
		WithData(feature.LoadClusterData).
		Load(); err != nil {
		return err
	} else {
		o.features = append(o.features, serviceMesh)
	}

	if dashboard, err := feature.CreateFeature("enable-service-mesh-for-dashboard").
		For(o.pluginSpec).
		WithConfig(o.config).
		AdditionalResources(feature.EnableServiceMeshInDashboard).
		Load(); err != nil {
		return err
	} else {
		o.features = append(o.features, dashboard)
	}

	if dataScienceProjects, err := feature.CreateFeature("migrate-data-science-projects").
		For(o.pluginSpec).
		WithConfig(o.config).
		// TODO this is not creating any resource - it is updating it
		AdditionalResources(feature.MigrateDataScienceProjects).
		Load(); err != nil {
		return err
	} else {
		o.features = append(o.features, dataScienceProjects)
	}

	if extAuthz, err := feature.CreateFeature("setup-external-authorization").
		For(o.pluginSpec).
		WithConfig(o.config).
		FromPaths(
			path.Join(rootDir, feature.AuthDir, "namespace.tmpl"),
			path.Join(rootDir, feature.AuthDir, "auth-smm.tmpl"),
			path.Join(rootDir, feature.AuthDir, "base"),
			path.Join(rootDir, feature.AuthDir, "rbac"),
			path.Join(rootDir, feature.AuthDir, "mesh-authz-ext-provider.patch.tmpl"),
		).
		WithData(feature.LoadClusterData).
		OnDelete(feature.RemoveExtensionProvider).
		Load(); err != nil {
		return err
	} else {
		o.features = append(o.features, extAuthz)
	}

	return nil
}

func (o *OssmInstaller) Generate(_ kftypesv3.ResourceEnum) error {
	var applyErrors *multierror.Error

	for _, f := range o.features {
		err := f.Apply()
		applyErrors = multierror.Append(applyErrors, err)
	}

	return applyErrors.ErrorOrNil()
}

func (o *OssmInstaller) CleanupResources() error {
	var cleanupErrors *multierror.Error
	for _, feature := range o.features {
		cleanupErrors = multierror.Append(cleanupErrors, feature.Cleanup())
	}

	return cleanupErrors.ErrorOrNil()
}

func internalError(err error) error {
	return &kfapisv3.KfError{
		Code:    int(kfapisv3.INTERNAL_ERROR),
		Message: fmt.Sprintf("%+v", err),
	}
}

// In order to process the templates, we need to create a tmp directory
// to store the files. This is because embedded files are read only.
// copyEmbeddedFS ensures that files embedded using go:embed are populated
// to dest directory
func copyEmbeddedFS(fsys fs.FS, root, dest string) error {
	return fs.WalkDir(fsys, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		destPath := filepath.Join(dest, path)
		if d.IsDir() {
			if err := os.MkdirAll(destPath, 0755); err != nil {
				return err
			}
		} else {
			data, err := fs.ReadFile(fsys, path)
			if err != nil {
				return err
			}
			if err := os.WriteFile(destPath, data, 0644); err != nil {
				return err
			}
		}

		return nil
	})
}
