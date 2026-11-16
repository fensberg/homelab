package parsed

import (
	"path"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"
)

// BuiltFrom is every directory of the tree a Go module at dir is built
// from: its own, and each one a replace directive points at by a path in
// this tree, as Go reads the go.mod. A module replaced by a version is
// fetched rather than read from here, and is not one.
func (r *Tree) BuiltFrom(t testing.TB, dir string) []string {
	t.Helper()
	rel := path.Join(dir, "go.mod")
	out, err := BuiltFromGoMod(dir, r.read(t, rel))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return out
}

// BuiltFromGoMod is BuiltFrom for a go.mod's contents, the module being at dir.
func BuiltFromGoMod(dir string, body []byte) ([]string, error) {
	f, err := modfile.Parse(path.Join(dir, "go.mod"), body, nil)
	if err != nil {
		return nil, err
	}
	out := []string{path.Clean(dir)}
	for _, r := range f.Replace {
		if r.New.Version != "" || !(strings.HasPrefix(r.New.Path, "./") || strings.HasPrefix(r.New.Path, "../")) {
			continue
		}
		out = append(out, path.Join(dir, r.New.Path))
	}
	return out, nil
}
