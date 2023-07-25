package ossm

import (
	"html/template"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type oAuth struct {
	AuthzEndpoint,
	TokenEndpoint,
	Route,
	ClientSecret,
	Hmac string
}

type manifest struct {
	name,
	path string
	template,
	patch,
	processed bool
}

// In order to process the templates, we need to create a tmp directory
// to store the files. This is because embedded files are read only.
var outputDir = "/tmp/ossm-installer/"

func ensureDirExists(dir string) error {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		err = os.MkdirAll(dir, 0755)
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *manifest) targetPath(kfdefName string, kfdefNs string) (string, error) {
	fullDir := filepath.Join(outputDir, kfdefNs, kfdefName, filepath.Dir(m.path))
	if err := ensureDirExists(fullDir); err != nil {
		return "", err
	}

	fileName := filepath.Base(m.path)
	fileNameWithoutExt := fileName[:len(fileName)-len(filepath.Ext(fileName))]
	return filepath.Join(fullDir, fileNameWithoutExt+".yaml"), nil
}

func (m *manifest) processTemplate(manifestRepo fs.FS, data interface{}, kfdefName string, kfdefNs string) error {
	if !m.template {
		return nil
	}
	// Create file in the regular filesystem, not the embedded one
	path, err := m.targetPath(kfdefName, kfdefNs)
	if err != nil {
		log.Error(err, "Failed to generate target path")
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		log.Error(err, "Failed to create file")
		return err
	}

	tmpl := template.New(m.name).
		Funcs(template.FuncMap{"ReplaceChar": ReplaceChar})

	// Read file from the embedded filesystem
	fileData, err := fs.ReadFile(manifestRepo, m.path)
	if err != nil {
		log.Error(err, "Failed to read fileData")
		return err
	}

	// Parse template from a string, not from a file
	tmpl, err = tmpl.Parse(string(fileData))
	if err != nil {
		return err
	}

	err = tmpl.Execute(f, data)
	m.processed = err == nil

	return err
}

func ReplaceChar(s string, oldChar, newChar string) string {
	return strings.ReplaceAll(s, oldChar, newChar)
}
