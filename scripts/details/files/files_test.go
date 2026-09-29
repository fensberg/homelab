package files

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteCreatesTheDirectoryItNeeds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "c.txt")
	if err := Write(path, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "x" {
		t.Fatalf("read back %q, %v", got, err)
	}
}
