package parsed

import (
	"os"
	"testing"

	"homelab/details/gitenv"
)

// These tests run git in trees of their own, so the developer's git
// configuration is pointed at nothing first.
func TestMain(m *testing.M) {
	gitenv.Isolate()
	os.Exit(m.Run())
}
