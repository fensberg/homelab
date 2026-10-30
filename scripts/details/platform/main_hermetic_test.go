package platform

import (
	"os"
	"testing"

	"homelab/details/gitenv"
)

// The tests here run git, so git reads nothing of the machine it runs on.
func TestMain(m *testing.M) {
	gitenv.Isolate()
	os.Exit(m.Run())
}
