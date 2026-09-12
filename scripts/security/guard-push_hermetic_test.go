package main

import (
	"os"
	"testing"
)

// TestMain cuts this package's tests off from the developer's git
// configuration.
//
// The tests here build fixture repositories and make commits in them, so
// anything git reads from the machine becomes an unstated input. That is not
// hypothetical: a fixture that makes a deliberately *unsigned* commit
// inherited `commit.gpgsign` from a global config and produced a signed one,
// so the test failed on every machine set up the way this repository tells
// people to set one up, and passed only on a machine configured the way it
// tells them not to.
//
// Pointing both config paths at /dev/null makes the fixtures depend on what
// the tests set and nothing else. Per-repository config still applies, which
// is correct - that is set by the fixture itself.
func TestMain(m *testing.M) {
	os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	os.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	os.Exit(m.Run())
}
