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
	tree.Write(t, "management/somewhere/first.tf", "# the root\n")
	tree.Write(t, "modules/something/second.tf", "# a module\n")
	tree.Write(t, "config/fixture.tpl.json", "{}\n")
	tree.Write(t, "README.md", "not what a plan is made from\n")
	tree.Write(t, "modules/infrastructure/release.json", `{"holds": ["modules/something", "elsewhere/read.env"]}`+"\n")
	tree.Write(t, "elsewhere/read.env", "A=1\n")
	tree.Index(t)
	return tree
}

// A plan is kept by what it is made from: a change there is a new key, and a
// change anywhere else, or nothing at all, is the same key.
func TestAPlanIsKeptByWhatItIsMadeFrom(t *testing.T) {
	tree := planned(t)
	key := func() string {
		t.Helper()
		k, err := tree.planKey(t, "site0", "")
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
	if other, err := tree.planKey(t, "site1", ""); err != nil || other == first {
		t.Errorf("another site's plans were kept under this site's key (%v)", err)
	}
	for _, rel := range []string{"management/somewhere/first.tf", "modules/something/second.tf", "config/fixture.tpl.json", "elsewhere/read.env"} {
		before := key()
		tree.Write(t, rel, "# changed\n")
		tree.Index(t)
		if key() == before {
			t.Errorf("a change to %s, which a plan is made from, kept the old key", rel)
		}
	}
}

// Planned with a release's modules, the plans are kept by the release and
// the roots: a change to the checkout's modules is not a new key, and a
// change to a root or to the release is.
func TestAPinnedPlanIsKeptByItsReleaseAndTheRoots(t *testing.T) {
	tree := planned(t)
	key := func(revision string) string {
		t.Helper()
		k, err := tree.planKey(t, "site0", revision)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	first := key("aaaa")
	if key("bbbb") == first {
		t.Error("another release gave the same key")
	}
	if key("") == first {
		t.Error("the checkout's own modules and a release's gave the same key")
	}
	tree.Write(t, "modules/something/second.tf", "# the checkout's module changed\n")
	tree.Index(t)
	if key("aaaa") != first {
		t.Error("a change to the checkout's modules gave a pinned plan a new key, though it plans with the release's")
	}
	tree.Write(t, "management/somewhere/first.tf", "# the root changed\n")
	tree.Index(t)
	if key("aaaa") == first {
		t.Error("a change to a root kept the pinned plan's old key")
	}
}

// A file the OpenTofu reads by a path written from its own directory is
// what the plan is made from, wherever in the repository it is: a root's,
// whichever modules are planned, and a module's only with the checkout's.
func TestAFileTheOpenTofuReadsIsWhatThePlanIsMadeFrom(t *testing.T) {
	tree := planned(t)
	tree.Write(t, "management/somewhere/picks.tf", `locals {
  listed = yamldecode(file("${path.module}/../../lists/listed.yml"))
  handed = file(var.handed)
  beyond = file("${path.module}/../../../outside")
}
`)
	tree.Write(t, "modules/something/picks.tf", `locals {
  pinned = file("${path.module}/../../lists/pinned.env")
}
`)
	tree.Write(t, "lists/listed.yml", "a: 1\n")
	tree.Write(t, "lists/pinned.env", "A=1\n")
	tree.Index(t)
	key := func(revision string) string {
		t.Helper()
		k, err := tree.planKey(t, "site0", revision)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	checkout, pinned := key(""), key("aaaa")
	tree.Write(t, "lists/listed.yml", "a: 2\n")
	tree.Index(t)
	if key("") == checkout || key("aaaa") == pinned {
		t.Error("a change to a file a root reads kept a plan's old key")
	}
	checkout, pinned = key(""), key("aaaa")
	tree.Write(t, "lists/pinned.env", "A=2\n")
	tree.Index(t)
	if key("") == checkout {
		t.Error("a change to a file the checkout's module reads kept the old key")
	}
	if key("aaaa") != pinned {
		t.Error("a change to a file the checkout's module reads gave a pinned plan a new key, though it plans with the release's")
	}
	reads := map[string]string{}
	for _, read := range tree.readByPath(t) {
		reads[read.File] = read.By
	}
	want := map[string]string{"lists/listed.yml": "management/somewhere/picks.tf", "lists/pinned.env": "modules/something/picks.tf"}
	if len(reads) != len(want) || reads["lists/listed.yml"] != want["lists/listed.yml"] || reads["lists/pinned.env"] != want["lists/pinned.env"] {
		t.Errorf("the files read were %v; want %v, and neither a path handed in nor one beyond the repository", reads, want)
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
	tree.Write(t, "management/somewhere/inputs.tf", `variable "config_path" {
  default = "../../config/fixture.rendered.json"
}
`)
	tree.Write(t, "management/plainly/inputs.tf", `variable "config_path" {
  type = string
}
`)
	tree.Index(t)
	if got, ok := tree.templateOf(t, "management/somewhere"); !ok || got != "config/fixture.tpl.json" {
		t.Errorf("a root's template was %q, %v; want config/fixture.tpl.json", got, ok)
	}
	if got, ok := tree.templateOf(t, "management/plainly"); ok {
		t.Errorf("a root with no default was given the template %q", got)
	}
	if got, ok := tree.templateOf(t, "management/absent"); ok {
		t.Errorf("a root that declares nothing was given the template %q", got)
	}
}
