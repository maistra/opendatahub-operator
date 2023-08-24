package ossm

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed templates
var embeddedFiles embed.FS

// In order to process the templates, we need to create a tmp directory
// to store the files. This is because embedded files are read only.
// copyEmbeddedFS ensures that files embedded using go:embed are populated
// to dest directory
func copyEmbeddedFiles(src, dest string) error {
	return fs.WalkDir(embeddedFiles, src, func(path string, dir fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		destPath := filepath.Join(dest, path)
		if dir.IsDir() {
			if err := os.MkdirAll(destPath, 0755); err != nil {
				return err
			}
		} else {
			data, err := fs.ReadFile(embeddedFiles, path)
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
