package repo

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"homelab/contractor/config"
	"homelab/tests/parsed"
)

// A hypervisor's facts are declared once, and never written into the code.
//
// Where a host keeps machine disks and downloaded images is a property of that
// host: a second hypervisor on LVM rather than ZFS names its storage otherwise.
// compute.tf once named both sixteen times, and compute.go, the API tests and
// the playbook repeated them, so describing a second kind of host meant editing
// code (epoch 02, "A hypervisor's own facts come from its config"). Each node's
// entry in the config now names its datastores, and the vnet the nodes sit on
// is the address plan's.
//
// Two checks, because either alone has a hole:
//
//   - every name the declarations hold appears nowhere else the estate runs
//     from, in code, comments or tests: a copy in a comment is the one the next
//     person pastes;
//   - no OpenTofu `datastore_id` or `bridge` is a quoted string, so a name
//     that was never declared anywhere cannot be written in either: a rule
//     over the plan's configuration (policy/conftest/plan_placements_not_written.rego).
//
// Documentation and the mutation ledger are not read: prose naming a Proxmox
// default explains it, and the ledger names what it plants to prove this.
var (
	// What declares a hypervisor fact, beside the address plan's own file -
	// which is found by what it declares (declPlanVNet).
	hypervisorFactFiles = map[string]string{
		"config/management.tpl.json": "each hypervisor node's datastores",
		// The devbox is a machine of its own, not part of any site, and this
		// is its own declaration of where it lives.
		"workstation/provision.yml": "where the devbox itself lives",
	}
	// The address plan declares the vnet a site's nodes sit on, beside its
	// network id: found by the file that writes that id.
	declPlanVNet = "vnet_vni"
)

func TestAHypervisorsFactsAreDeclaredOnceAndNeverInTheCode(t *testing.T) {
	heavy(t, "semgrep reads every file for the hypervisor's names, a few seconds")
	tree := repository(t)
	planFile := writing(t, tree, declPlanVNet).Path
	names := declaredHypervisorFacts(t, tree, planFile)
	if len(names) < 2 {
		t.Fatalf("only %d hypervisor fact(s) were read from the declarations, so this checked almost nothing: %v", len(names), names)
	}

	// The files that declare the facts are where the names belong.
	declarations := map[string]bool{planFile: true}
	for rel := range hypervisorFactFiles {
		declarations[rel] = true
	}
	files := tracked(t, func(rel string) bool {
		if declarations[rel] {
			return false
		}
		// Prose explains, and the mutation ledger has to name what it plants
		// to prove this guard.
		if strings.HasPrefix(rel, "docs/") || strings.HasSuffix(rel, ".md") || isLedger(rel) {
			return false
		}
		switch filepath.Ext(rel) {
		case ".go", ".tf", ".hcl", ".yml", ".yaml", ".json", ".sh", ".j2", ".py", ".env":
			return true
		}
		return false
	})
	if len(files) < 50 {
		t.Fatalf("only %d files were read, so the enumeration has stopped matching", len(files))
	}

	found := hypervisorFactsIn(t, tree, files, names)
	sort.Strings(found)
	for _, f := range found {
		t.Error(f)
	}
}

// writing is the one OpenTofu file that writes an argument or a key of that
// name, wherever it is.
func writing(t *testing.T, tree *parsed.Tree, name string) parsed.OpenTofuFile {
	t.Helper()
	var found []parsed.OpenTofuFile
	for _, f := range tree.OpenTofu(t) {
		has := slices.ContainsFunc(objectItems(f.Body), func(item hclsyntax.ObjectConsItem) bool { return parsed.KeyName(item) == name })
		_ = hclsyntax.VisitAll(f.Body, func(n hclsyntax.Node) hcl.Diagnostics {
			if a, ok := n.(*hclsyntax.Attribute); ok && a.Name == name {
				has = true
			}
			return nil
		})
		if has {
			found = append(found, f)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d OpenTofu files write %s, and one was expected", len(found), name)
	}
	return found[0]
}

// declaredHypervisorFacts reads every name the declarations hold: each node's
// datastores in the config template, and the address plan's vnet.
func declaredHypervisorFacts(t *testing.T, tree *parsed.Tree, planFile string) []string {
	t.Helper()
	// Decoded with the config's own types, so the shape is the contractor's
	// and not a second copy of it.
	var tpl config.Config
	tree.JSONInto(t, "config/management.tpl.json", &tpl)
	seen := map[string]bool{}
	for _, s := range tpl.Sites {
		for _, n := range s.Hypervisor.Nodes {
			seen[n.Datastores.Disks] = true
			seen[n.Datastores.Images] = true
		}
	}
	for _, f := range tree.OpenTofu(t) {
		if f.Path != planFile {
			continue
		}
		for _, item := range objectItems(f.Body) {
			if parsed.KeyName(item) != "vnet" {
				continue
			}
			if v, ok := parsed.Literal(item.ValueExpr); ok && !v.IsNull() && v.Type() == cty.String {
				seen[v.AsString()] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		if strings.TrimSpace(n) != "" {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// hypervisorFactsIn is each place in the files that names one of the facts,
// comments and all: a name written down twice is a host that cannot be
// described without editing it, wherever it is written.
func hypervisorFactsIn(t *testing.T, tree *parsed.Tree, files, names []string) []string {
	t.Helper()
	var rules []map[string]any
	for i, n := range names {
		rules = append(rules, map[string]any{
			"id": fmt.Sprintf("hypervisor-fact-%d", i), "languages": []string{"generic"}, "severity": "INFO",
			"message": n, "metadata": map[string]any{"about": n},
			"pattern-regex": `(^|[^A-Za-z0-9_-])` + regexp.QuoteMeta(n) + `($|[^A-Za-z0-9_-])`,
		})
	}
	var out []string
	for _, f := range scanText(t, tree, rules, files) {
		out = append(out, fmt.Sprintf("%s:%d names %q, a hypervisor fact the config declares. Read it from the declaration instead (a node's datastores, or the address plan's vnet).", f.Path, f.Line, f.About))
	}
	return out
}

func TestHypervisorFactsInCatchesEachWayToWriteOne(t *testing.T) {
	heavy(t, "semgrep reads the fixture's files")
	names := []string{"tank-images", "vnetx"}
	for _, c := range []struct {
		name, rel, body string
		want            bool
	}{
		{"a Go string", "a.go", "package a\n\nconst p = \"tank-images:iso/talos-\"\n", true},
		{"a comment", "a.tf", "# lives in tank-images\n", true},
		{"a playbook value", "a.yml", "sdn_vnet: vnetx\n", true},
		{"a longer name is another name", "a.go", "package a\n\nconst p = \"tank-images-old\"\n", false},
	} {
		tree := parsed.Empty(t)
		tree.Write(t, c.rel, c.body)
		tree.Index(t)
		found := hypervisorFactsIn(t, tree, []string{c.rel}, names)
		if got := len(found) > 0; got != c.want {
			t.Errorf("%s: found=%v, want %v: %v", c.name, got, c.want, found)
		}
	}
}
