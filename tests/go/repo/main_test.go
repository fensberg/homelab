package repo

import (
	"os"
	"testing"

	"homelab/tests/parsed"
)

// The guards start where no relative path reaches the repository, and read
// it through tests/parsed alone.
func TestMain(m *testing.M) {
	os.Exit(parsed.Main(m))
}
