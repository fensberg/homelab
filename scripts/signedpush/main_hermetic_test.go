package main

import (
	"os"
	"testing"

	"homelab/details/gitenv"
)

// TestMain cuts this package's tests off from the developer's git
// configuration, for the reason recorded in CLAUDE.md: a test that reads
// anything from the machine rather than setting it reports the machine.
//
// These tests build fixture repositories and run git in them, so a global
// setting - commit.gpgsign is the one that has already caught this repository
// out twice - silently changes what the fixture is. Per-repository config
// still applies, which is correct: the fixture sets that itself.
func TestMain(m *testing.M) {
	gitenv.Isolate()
	os.Exit(m.Run())
}
