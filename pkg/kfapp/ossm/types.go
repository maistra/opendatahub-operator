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
const outputDir = "/tmp/ossm-installer/"

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

func (m *manifest) processTemplate(data interface{}, kfdefName string, kfdefNs string) error {
	if !m.template {
		return nil
	}
	// Create yaml file in the regular filesystem
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

	// Parse template from .tmpl file
	tmpl, err = tmpl.ParseFiles(strings.Replace(path, ".yaml", ".tmpl", 1))
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
