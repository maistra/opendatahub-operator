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
	BaseOutputDir   = "/tmp/opendatahub-manifests/"
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

	// Walk through the embedded file system starting from the specified root path
	err := fs.WalkDir(embeddedFS, rootPath, func(path string, dirEntry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if dirEntry.IsDir() {
			return nil
		}

		// Construct the manifest object from the path
		_, err = fs.ReadFile(embeddedFS, path)
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

	m.processedContent = string(buffer.Bytes()) // Save processed content in manifest struct
	m.processed = true

	return nil
}
