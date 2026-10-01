package pin

import (
	"os"
	"testing"

	"homelab/details/gitenv"
)

// TestMain cuts this package's tests off from the developer's git
// configuration: they build a repository in a temporary directory and commit
// to it, and a signing setting or a hook from the machine would decide
// whether that works.
func TestMain(m *testing.M) {
	gitenv.Isolate()
	os.Exit(m.Run())
}
