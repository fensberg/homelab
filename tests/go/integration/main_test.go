//go:build integration

package integration

import (
	"os"
	"testing"
)

// The developer's git configuration must not reach this tier.
//
// forkable_test.go asks git for the tracked set rather than walking the
// filesystem, which is what stops a rendered, gitignored artifact being
// reported as committed. That makes this package one that shells out to git,
// and tests/go/repo requires any such package to neutralise the machine's
// configuration first - a fixture that inherits whatever is set globally
// reports the machine rather than the code, and commit.gpgsign is the one
// that has already caused exactly that here.
//
// Both config paths point at /dev/null, so this tier depends on what it sets
// and nothing else. Per-repository configuration still applies, which is
// correct: that belongs to the repository being examined.
func TestMain(m *testing.M) {
	os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	os.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	os.Exit(m.Run())
}
