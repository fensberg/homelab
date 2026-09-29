package gitenv

import (
	"os"
	"testing"
)

func TestIsolatePointsBothConfigsAtNothing(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/home/someone/.gitconfig")
	t.Setenv("GIT_CONFIG_SYSTEM", "/etc/gitconfig")
	Isolate()
	for _, k := range []string{"GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM"} {
		if got := os.Getenv(k); got != os.DevNull {
			t.Errorf("%s is %q, want %q", k, got, os.DevNull)
		}
	}
}
