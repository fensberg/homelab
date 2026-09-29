// Package files writes a file whose directory may not exist yet.
package files

import (
	"os"
	"path/filepath"
)

// Write writes body to path, creating its directory first. Files are 0644 and
// directories 0755: this is for fixtures and generated files, not secrets.
func Write(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o644)
}
