// Package gitenv isolates a test process from the machine's git configuration.
//
// A test that runs git inherits whatever the developer's global and system
// configuration say - commit.gpgsign has already made a fixture report the
// machine rather than the code - so every package that runs git in its tests
// calls Isolate from its TestMain, and tests/go/repo requires it.
package gitenv

import "os"

// Isolate points git's global and system configuration at nothing. The
// repository's own configuration still applies, which is correct: it belongs
// to the repository being examined.
func Isolate() {
	_ = os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	_ = os.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
}

// IsCommit reports whether s is a full commit hash: forty lower-case
// hexadecimal digits. A short hash, a branch and a tag are each a name for
// whatever they point at today, and the things that pin to a commit - a
// site's modules, an action's version - must not accept one.
func IsCommit(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
