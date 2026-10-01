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

func TestIsCommitAcceptsOnlyAFullLowerCaseHash(t *testing.T) {
	full := "0123456789abcdef0123456789abcdef01234567"
	for s, want := range map[string]bool{
		full:           true,
		full[:39]:      false,
		full + "0":     false,
		"0123456":      false,
		"main":         false,
		"v1.2.3":       false,
		"":             false,
		"G" + full[1:]: false,
		"0123456789ABCDEF0123456789ABCDEF01234567": false,
	} {
		if got := IsCommit(s); got != want {
			t.Errorf("IsCommit(%q) = %v, want %v", s, got, want)
		}
	}
}
