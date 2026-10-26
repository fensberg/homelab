package parsed

import (
	"path"
	"testing"

	"homelab/tests/harness"
)

// A Flux object is found by what it declares, and only when one manifest
// declares it.
func TestAFluxObjectIsFoundByWhatItDeclaresAndOnlyWhenOneDoes(t *testing.T) {
	tree := Empty(t)
	first, second := path.Join(harness.FluxTree, "layer", "first.yaml"), path.Join(harness.FluxTree, "layer", "second.yml")
	tree.Write(t, first, "kind: ConfigMap\nmetadata:\n  name: once\n---\nkind: Secret\nmetadata:\n  name: twice\n")
	tree.Write(t, second, "kind: Secret\nmetadata:\n  name: twice\n")
	tree.Write(t, "elsewhere/c.yaml", "kind: ConfigMap\nmetadata:\n  name: outside\n")
	tree.Index(t)

	if path, docs := tree.FluxObject(t, "ConfigMap", "once"); path != first || len(docs) != 2 {
		t.Errorf("the object declared once was found in %s with %d documents", path, len(docs))
	}
	if _, err := tree.FluxObjectPath(t, "Secret", "twice"); err == nil {
		t.Error("an object two manifests declare was given one file")
	}
	if _, err := tree.FluxObjectPath(t, "ConfigMap", "outside"); err == nil {
		t.Error("an object outside the Flux tree was found in it")
	}
	if !refuses(t, func(tb testing.TB) { tree.FluxObject(tb, "ConfigMap", "missing") }) {
		t.Error("an object nothing declares did not fail the test asking for it")
	}
}

// The suppliers list and the deliveries lock are read as the estate's own
// readers read them, and one that does not read fails the test asking.
func TestTheSuppliersListAndTheLockAreReadAsTheirOwnReadersReadThem(t *testing.T) {
	tree := Empty(t)
	tree.Write(t, "scripts/approved-suppliers.yml", `pull_request_pools:
  names: []
tools:
  - source: example/tool
    version: TOOL_VERSION
egress: []
work: []
`)
	tree.Write(t, "scripts/deliveries.lock", "# [fetch: tool TOOL_VERSION=1.0.0]\n# [end]\n")
	tree.Index(t)

	if tools := tree.Tools(t); len(tools) != 1 {
		t.Errorf("read %d tools, and one is declared", len(tools))
	}
	if got := tree.Egress(t); len(got) != 0 {
		t.Errorf("read %d egress entries from an empty list", len(got))
	}
	if got := tree.Work(t); len(got) != 0 {
		t.Errorf("read %d work entries from an empty list", len(got))
	}
	_ = tree.Pools(t)
	if lock := tree.DeliveriesLock(t); len(lock) != 1 || lock["fetch:tool"].Version != "1.0.0" {
		t.Errorf("the lock was read as %v", lock)
	}

	broken := Empty(t)
	broken.Write(t, "scripts/approved-suppliers.yml", "tools:\n  - just a string\negress:\n  - 3\nwork:\n  - kind: nonsense\n    job: j\n")
	broken.Write(t, "scripts/deliveries.lock", "# [end]\n")
	broken.Index(t)
	for what, read := range map[string]func(testing.TB){
		"tools":  func(tb testing.TB) { broken.Tools(tb) },
		"egress": func(tb testing.TB) { broken.Egress(tb) },
		"work":   func(tb testing.TB) { broken.Work(tb) },
		"lock":   func(tb testing.TB) { broken.DeliveriesLock(tb) },
	} {
		if !refuses(t, read) {
			t.Errorf("a %s that does not read was read", what)
		}
	}
}
