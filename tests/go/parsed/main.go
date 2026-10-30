package parsed

import (
	"fmt"
	"os"
	"testing"

	"homelab/details/gitenv"
)

// Main runs a package of guards. They start in an empty directory of their
// own, so that no path a guard writes relative to where it runs - a literal,
// or one held in a variable - reaches a file of the repository: what the
// repository holds is reached through a Tree, and only through one. And the
// machine's git configuration is pointed at nothing, since every listing of a
// tree is git's.
func Main(m *testing.M) int {
	gitenv.Isolate()
	dir, err := os.MkdirTemp("", "guards")
	if err != nil {
		fmt.Fprintf(os.Stderr, "making the directory the guards start in: %v\n", err)
		return 1
	}
	defer os.RemoveAll(dir)
	if err := os.Chdir(dir); err != nil {
		fmt.Fprintf(os.Stderr, "starting the guards in %s: %v\n", dir, err)
		return 1
	}
	return m.Run()
}
