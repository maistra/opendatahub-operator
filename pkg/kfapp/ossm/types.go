package ossm

import (
	"fmt"
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
var outputDir = "/tmp/templates/"

func ensureDirExists(dir string) error {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		err = os.MkdirAll(dir, 0755)
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *manifest) targetPath() (string, error) {
	if err := ensureDirExists(outputDir); err != nil {
		return "", err
	}
	fileName := filepath.Base(m.path)
	fileNameWithoutExt := fileName[:len(fileName)-len(filepath.Ext(fileName))]
	return filepath.Join(outputDir, fileNameWithoutExt+".yaml"), nil
}

func (m *manifest) processTemplate(manifestRepo fs.FS, data interface{}) error {
	if !m.template {
		return nil
	}
	// Create file in the regular filesystem, not the embedded one
	path, err := m.targetPath()
	if err != nil {
		log.Error(err, "Failed to generate target path")
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		log.Error(err, "Failed to create file, 59")
		return err
	}

	tmpl := template.New(m.name).
		Funcs(template.FuncMap{"ReplaceChar": ReplaceChar})

	// Read file from the embedded filesystem
	fmt.Printf("Reading file: %s\n", m.name)
	fmt.Printf("found: %s\n", m.path)
	fileData, err := fs.ReadFile(manifestRepo, m.path)
	if err != nil {
		log.Error(err, "Failed to read fileData, 70")
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
