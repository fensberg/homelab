package tests

import (
	"os"
	"path/filepath"
	"testing"

	"homelab/details/applications"
)

// readManifest reads one of the application's manifests, by its path inside
// the application's directory, into out: one element per document. The
// repository's guards hand them over as JSON (applications.ReadManifest).
func readManifest(t *testing.T, rel string, out any) {
	t.Helper()
	if err := applications.ReadManifest(rel, out); err != nil {
		t.Fatal(err)
	}
}

// write puts body at path, making the directories above it.
func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
