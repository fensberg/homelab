package parsed

import (
	"strings"
	"testing"
)

// A module is built from its own directory and every directory of the tree
// a replace points at; one replaced by a version, or by a module path, is
// fetched, and is not.
func TestAModuleIsBuiltFromItsOwnDirectoryAndEveryLocalReplace(t *testing.T) {
	got, err := BuiltFromGoMod("scripts/prog", []byte(`module homelab/prog

go 1.25

require homelab/shared v0.0.0

replace homelab/shared => ../shared

replace example.test/far => example.test/near v1.2.3

replace example.test/here => ./vendored/here
`))
	if err != nil {
		t.Fatal(err)
	}
	want := "scripts/prog,scripts/shared,scripts/prog/vendored/here"
	if strings.Join(got, ",") != want {
		t.Errorf("built from %v, want %s", got, want)
	}
	if _, err := BuiltFromGoMod("x", []byte("module")); err == nil {
		t.Error("a go.mod Go cannot read was read")
	}
}
