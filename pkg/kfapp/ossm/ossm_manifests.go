package ossm

import (
	"embed"
	"fmt"
	configtypes "github.com/opendatahub-io/opendatahub-operator/apis/config"
	"github.com/opendatahub-io/opendatahub-operator/pkg/kfconfig/ossmplugin"
	"github.com/opendatahub-io/opendatahub-operator/pkg/secret"
	"github.com/pkg/errors"
	"io/fs"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"path"
	"path/filepath"
	"strings"
)

//go:embed templates
var embeddedFiles embed.FS

type applier func(config *rest.Config, filename string, elems ...configtypes.NameValue) error

func (o *OssmInstaller) applyManifests() error {
	var apply applier

	for _, m := range o.manifests {
		targetPath, err := m.targetPath(o.Name, o.Namespace)
		if err != nil {
			log.Error(err, "Error generating target path")
			return err
		}
		if m.patch {
			apply = func(config *rest.Config, filename string, elems ...configtypes.NameValue) error {
				log.Info("patching using manifest", "name", m.name, "path", targetPath)
				return o.PatchResourceFromFile(filename, elems...)
			}
		} else {
			apply = func(config *rest.Config, filename string, elems ...configtypes.NameValue) error {
				log.Info("applying manifest", "name", m.name, "path", targetPath)
				return o.CreateResourceFromFile(filename, elems...)
			}
		}

		err = apply(
			o.config,
			targetPath,
		)

		if err != nil {
			log.Error(err, "failed to create resource", "name", m.name, "path", targetPath)
			return err
		}
	}

	return nil
}

func (o *OssmInstaller) processManifests() error {
	if err := o.SyncCache(); err != nil {
		return internalError(err)
	}
	ControlPlaneDir := "templates/control-plane"
	AuthDir := "templates/authorino"

	// TODO warn when file is not present instead of throwing an error
	// IMPORTANT: Order of locations from where we load manifests/templates to process is significant
	err := o.loadManifestsFrom(
		path.Join(ControlPlaneDir, "base"),
		path.Join(ControlPlaneDir, "filters"),
		path.Join(ControlPlaneDir, "oauth"),
		path.Join(ControlPlaneDir, "smm.tmpl"),
		path.Join(ControlPlaneDir, "namespace.patch.tmpl"),

		path.Join(AuthDir, "namespace.tmpl"),
		path.Join(AuthDir, "auth-smm.tmpl"),
		path.Join(AuthDir, "base"),
		path.Join(AuthDir, "rbac"),
		path.Join(AuthDir, "mesh-authz-ext-provider.patch.tmpl"),
	)
	if err != nil {
		return internalError(errors.WithStack(err))
	}

	data, err := o.prepareTemplateData()
	if err != nil {
		return internalError(errors.WithStack(err))
	}

	for i, m := range o.manifests {
		if err := m.processTemplate(embeddedFiles, data, o.Name, o.Namespace); err != nil {
			return internalError(errors.WithStack(err))
		}

		fmt.Printf("%d: %+v\n", i, m)
	}

	return nil
}

func (o *OssmInstaller) loadManifestsFrom(paths ...string) error {
	var err error
	var manifests []manifest
	var manifestRepo = embeddedFiles
	for _, p := range paths {
		manifests, err = loadManifestsFrom(manifestRepo, manifests, p)
		if err != nil {
			return internalError(errors.WithStack(err))
		}
	}

	o.manifests = manifests

	return nil
}

func loadManifestsFrom(manifestRepo fs.FS, manifests []manifest, path string) ([]manifest, error) {
	f, err := manifestRepo.Open(path)
	if err != nil {
		return nil, internalError(errors.WithStack(err))
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, internalError(errors.WithStack(err))
	}

	if info.IsDir() {
		// It's a directory, so walk it
		dirFS, err := fs.Sub(manifestRepo, path)
		if err != nil {
			return nil, internalError(errors.WithStack(err))
		}

		err = fs.WalkDir(dirFS, ".", func(relativePath string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			fullPath := filepath.Join(path, relativePath)
			basePath := filepath.Base(relativePath)
			manifests = append(manifests, manifest{
				name:     basePath,
				path:     fullPath,
				patch:    strings.Contains(basePath, ".patch"),
				template: filepath.Ext(relativePath) == ".tmpl",
			})
			return nil
		})
		if err != nil {
			return nil, internalError(errors.WithStack(err))
		}
	} else {
		// It's a file, so handle it directly
		basePath := filepath.Base(path)
		manifests = append(manifests, manifest{
			name:     basePath,
			path:     path,
			patch:    strings.Contains(basePath, ".patch"),
			template: filepath.Ext(path) == ".tmpl",
		})
	}

	return manifests, nil
}

// TODO(smell) this is now holding two responsibilities:
// - creates data structure to be fed to templates
// - creates secrets using k8s API calls
func (o *OssmInstaller) prepareTemplateData() (interface{}, error) {
	data := struct {
		*ossmplugin.OssmPluginSpec
		OAuth oAuth
		Domain,
		AppNamespace string
	}{
		AppNamespace: o.KfConfig.Namespace,
	}

	spec, err := o.GetPluginSpec()
	if err != nil {
		return nil, internalError(errors.WithStack(err))
	}
	data.OssmPluginSpec = spec

	if domain, err := GetDomain(o.config); err == nil {
		data.Domain = domain
	} else {
		return nil, internalError(errors.WithStack(err))
	}

	var clientSecret, hmac *secret.Secret
	if clientSecret, err = secret.NewSecret("ossm-odh-oauth", "random", 32); err != nil {
		return nil, internalError(errors.WithStack(err))
	}

	if hmac, err = secret.NewSecret("ossm-odh-hmac", "random", 32); err != nil {
		return nil, internalError(errors.WithStack(err))
	}

	if oauthServerDetailsJson, err := GetOAuthServerDetails(); err == nil {
		data.OAuth = oAuth{
			AuthzEndpoint: oauthServerDetailsJson.Get("authorization_endpoint").MustString("authorization_endpoint"),
			TokenEndpoint: oauthServerDetailsJson.Get("token_endpoint").MustString("token_endpoint"),
			Route:         ExtractHostName(oauthServerDetailsJson.Get("issuer").MustString("issuer")),
			ClientSecret:  clientSecret.Value,
			Hmac:          hmac.Value,
		}
	} else {
		return nil, internalError(errors.WithStack(err))
	}

	if spec.Mesh.Certificate.Generate {
		if err := o.createSelfSignedCerts(data.Domain, metav1.ObjectMeta{
			Name:      spec.Mesh.Certificate.Name,
			Namespace: spec.Mesh.Namespace,
		}); err != nil {
			return nil, internalError(err)
		}
	}

	if err := o.createEnvoySecret(data.OAuth, metav1.ObjectMeta{
		Name:      data.AppNamespace + "-oauth2-tokens",
		Namespace: data.Mesh.Namespace,
	}); err != nil {
		return nil, internalError(err)
	}

	return data, nil
}
