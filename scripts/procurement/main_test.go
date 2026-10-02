package main

import (
	"os"
	"testing"

	"homelab/details/gitenv"
)

// TestMain cuts this package's tests off from the developer's git
// configuration: the tests of deliver-modules make real commits, and a
// fixture that inherited commit.gpgsign would report the machine rather than
// the code.
func TestMain(m *testing.M) {
	gitenv.Isolate()
	os.Exit(m.Run())
}
