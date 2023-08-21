package ossm

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type oAuth struct {
	AuthzEndpoint,
	TokenEndpoint,
	Route,
	Port,
	ClientSecret,
	Hmac string
}

func ReplaceChar(s string, oldChar, newChar string) string {
	return strings.ReplaceAll(s, oldChar, newChar)
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
