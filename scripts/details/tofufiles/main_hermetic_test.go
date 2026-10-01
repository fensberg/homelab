package tofufiles

import (
	"os"
	"testing"

	"homelab/details/gitenv"
)

// TestMain cuts this package's tests off from the developer's git
// configuration. They build a repository in a temporary directory and ask git
// what it tracks, and a test that reads anything from the machine rather than
// setting it reports the machine.
func TestMain(m *testing.M) {
	gitenv.Isolate()
	os.Exit(m.Run())
}
