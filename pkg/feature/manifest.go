package feature

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"path/filepath"
	"strings"
)

//go:embed templates
var embeddedFiles embed.FS

const (
	BaseDir         = "templates/servicemesh/"
	ControlPlaneDir = BaseDir + "control-plane"
	AuthDir         = BaseDir + "authorino"
	MonitoringDir   = BaseDir + "monitoring"
)

type manifest struct {
	name,
	path string
	template,
	patch,
	processed bool
	processedContent string
}

func loadManifestsFrom(embeddedFS embed.FS, rootPath string) ([]manifest, error) {
	var manifests []manifest

	err := fs.WalkDir(embeddedFS, rootPath, func(path string, dirEntry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if dirEntry.IsDir() {
			return nil
		}

		_, err = fs.ReadFile(embeddedFS, path)
		if err != nil {
			log.Error(err, "Failed to load manifest from", "path", path)
			return err
		}
		m := loadManifestFrom(path)
		manifests = append(manifests, m)

		return nil
	})

	if err != nil {
		return nil, err
	}

	return manifests, nil
}

func loadManifestFrom(path string) manifest {
	basePath := filepath.Base(path)
	m := manifest{
		name:     basePath,
		path:     path,
		patch:    strings.Contains(basePath, ".patch"),
		template: filepath.Ext(path) == ".tmpl",
	}

	return m
}

func (m *manifest) targetPath() string {
	return fmt.Sprintf("%s%s", m.path[:len(m.path)-len(filepath.Ext(m.path))], ".yaml")
}

func (m *manifest) processTemplate(fs embed.FS, data interface{}) error {
	if !m.template {
		return nil
	}

	templateContent, err := fs.ReadFile(m.path)
	if err != nil {
		log.Error(err, "Failed to read template file", "path", m.path)
		return err
	}

	tmpl, err := template.New(m.name).Funcs(template.FuncMap{"ReplaceChar": ReplaceChar}).Parse(string(templateContent))
	if err != nil {
		log.Error(err, "Failed to template for file", "path", m.path)
		return err
	}

	var buffer bytes.Buffer
	if err := tmpl.Execute(&buffer, data); err != nil {
		return err
	}

	m.processedContent = buffer.String()
	m.processed = true

	return nil
}
