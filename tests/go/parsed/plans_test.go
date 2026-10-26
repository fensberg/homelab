package parsed

import (
	"os"
	"path/filepath"
	"testing"

	"homelab/contractor/config"
)

// planned is a fixture tree holding what a site's plans are made from, and
// what they are not.
func planned(t *testing.T) *Tree {
	t.Helper()
	tree := Empty(t)
	tree.Write(t, "management/cluster/main.tf", "# the root\n")
	tree.Write(t, "modules/infrastructure/m/main.tf", "# a module\n")
	tree.Write(t, "config/management.tpl.json", "{}\n")
	tree.Write(t, "README.md", "not what a plan is made from\n")
	tree.Index(t)
	return tree
}

// A plan is kept by what it is made from: a change there is a new key, and a
// change anywhere else, or nothing at all, is the same key.
func TestAPlanIsKeptByWhatItIsMadeFrom(t *testing.T) {
	tree := planned(t)
	key := func() string {
		t.Helper()
		k, err := tree.planKey(t, "site0")
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	first := key()
	if again := key(); again != first {
		t.Errorf("the same files gave two keys: %s and %s", first, again)
	}
	tree.Write(t, "README.md", "changed, and nothing a plan reads\n")
	tree.Index(t)
	if got := key(); got != first {
		t.Errorf("a change to a file no plan reads gave a new key")
	}
	if other, err := tree.planKey(t, "site1"); err != nil || other == first {
		t.Errorf("another site's plans were kept under this site's key (%v)", err)
	}
	for _, rel := range []string{"management/cluster/main.tf", "modules/infrastructure/m/main.tf", "config/management.tpl.json"} {
		before := key()
		tree.Write(t, rel, "# changed\n")
		tree.Index(t)
		if key() == before {
			t.Errorf("a change to %s, which a plan is made from, kept the old key", rel)
		}
	}
}

// Kept plans are a site's every root, or none: a plan of some of its roots
// is not its plan.
func TestKeptPlansAreEveryRootOrNone(t *testing.T) {
	dir := t.TempDir()
	if kept, err := readKept(dir); kept != nil || err != nil {
		t.Errorf("an empty directory gave %v, %v; want nothing kept", kept, err)
	}
	if err := os.WriteFile(filepath.Join(dir, config.ClusterRoot+".json"), []byte(`{"cluster":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if kept, err := readKept(dir); kept != nil || err != nil {
		t.Errorf("one root of two gave %v, %v; want nothing kept", kept, err)
	}
	if err := os.WriteFile(filepath.Join(dir, config.PlatformRoot+".json"), []byte(`{"platform":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	kept, err := readKept(dir)
	if err != nil || len(kept) != 2 || kept[0].Root != config.ClusterRoot || string(kept[1].Plan) != `{"platform":true}` {
		t.Errorf("both roots gave %v, %v", kept, err)
	}
}

// A root's template is found from what it reads: the default of its
// config_path, named for the template it is rendered from.
func TestARootsTemplateIsTheOneItsConfigIsRenderedFrom(t *testing.T) {
	tree := Empty(t)
	tree.Write(t, "management/estate/variables.tf", `variable "config_path" {
  default = "../../config/estate.rendered.json"
}
`)
	tree.Write(t, "management/plain/variables.tf", `variable "config_path" {
  type = string
}
`)
	tree.Index(t)
	if got, ok := tree.templateOf(t, "management/estate"); !ok || got != "config/estate.tpl.json" {
		t.Errorf("the estate root's template was %q, %v; want config/estate.tpl.json", got, ok)
	}
	if got, ok := tree.templateOf(t, "management/plain"); ok {
		t.Errorf("a root with no default was given the template %q", got)
	}
	if got, ok := tree.templateOf(t, "management/absent"); ok {
		t.Errorf("a root that declares nothing was given the template %q", got)
	}
}
