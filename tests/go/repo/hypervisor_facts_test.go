package repo

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"homelab/contractor/config"
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
//     that was never declared anywhere cannot be written in either.
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
	// network id.
	declPlanVNet     = "vnet_vni ="
	planVNet         = regexp.MustCompile(`(?m)^\s*vnet\s*=\s*"([^"]+)"`)
	literalPlacement = regexp.MustCompile(`(?m)^\s*(datastore_id|bridge)\s*=\s*"`)
)

func TestAHypervisorsFactsAreDeclaredOnceAndNeverInTheCode(t *testing.T) {
	root := repoRoot(t)

	names := declaredHypervisorFacts(t, root)
	if len(names) < 2 {
		t.Fatalf("only %d hypervisor fact(s) were read from the declarations, so this checked almost nothing: %v", len(names), names)
	}
	var patterns []*regexp.Regexp
	for _, n := range names {
		patterns = append(patterns, regexp.MustCompile(`(^|[^A-Za-z0-9_-])`+regexp.QuoteMeta(n)+`($|[^A-Za-z0-9_-])`))
	}

	// The files that declare the facts are where the names belong.
	declarations := map[string]bool{}
	for rel := range hypervisorFactFiles {
		declarations[rel] = true
	}
	planFile, _ := tofuDeclaring(t, declPlanVNet)
	declarations[planFile] = true

	files := tracked(t, func(rel string) bool {
		if declarations[rel] {
			return false
		}
		// Prose explains, and the mutation ledger has to name what it plants
		// to prove this guard.
		if strings.HasPrefix(rel, "docs/") || strings.HasSuffix(rel, ".md") || rel == "tests/mutations.yml" {
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

	var found []string
	for _, rel := range files {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		found = append(found, hypervisorFactsIn(rel, string(body), names, patterns)...)
	}
	sort.Strings(found)
	for _, f := range found {
		t.Error(f)
	}
}

// declaredHypervisorFacts reads every name the declarations hold: each node's
// datastores in the config template, and the address plan's vnet.
func declaredHypervisorFacts(t *testing.T, root string) []string {
	t.Helper()
	// Decoded with the config's own types, so the shape is the contractor's
	// and not a second copy of it.
	var tpl config.Config
	if err := json.Unmarshal([]byte(readRepoFile(t, "config/management.tpl.json")), &tpl); err != nil {
		t.Fatalf("config/management.tpl.json: %v", err)
	}
	seen := map[string]bool{}
	for _, s := range tpl.Sites {
		for _, n := range s.Hypervisor.Nodes {
			seen[n.Datastores.Disks] = true
			seen[n.Datastores.Images] = true
		}
	}
	_, plan := tofuDeclaring(t, declPlanVNet)
	for _, m := range planVNet.FindAllStringSubmatch(plan, -1) {
		seen[m[1]] = true
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

// hypervisorFactsIn is what one file gets wrong.
func hypervisorFactsIn(rel, body string, names []string, patterns []*regexp.Regexp) []string {
	var out []string
	for i, line := range strings.Split(body, "\n") {
		for j, p := range patterns {
			if p.MatchString(line) {
				out = append(out, fmt.Sprintf("%s:%d names %q, a hypervisor fact the config declares. Read it from the declaration instead (a node's datastores, or the address plan's vnet).", rel, i+1, names[j]))
			}
		}
		if (strings.HasSuffix(rel, ".tf")) && literalPlacement.MatchString(line) {
			out = append(out, fmt.Sprintf("%s:%d writes a Proxmox storage or network as a quoted string. Read it from the hypervisor node's datastores or the address plan.", rel, i+1))
		}
	}
	return out
}

func TestHypervisorFactsInCatchesEachWayToWriteOne(t *testing.T) {
	names := []string{"tank-images", "vnetx"}
	var patterns []*regexp.Regexp
	for _, n := range names {
		patterns = append(patterns, regexp.MustCompile(`(^|[^A-Za-z0-9_-])`+regexp.QuoteMeta(n)+`($|[^A-Za-z0-9_-])`))
	}
	for _, c := range []struct {
		name, rel, body string
		want            bool
	}{
		{"a Go string", "a.go", `const p = "tank-images:iso/talos-"`, true},
		{"a comment", "a.tf", `# lives in tank-images`, true},
		{"a playbook value", "a.yml", `sdn_vnet: vnetx`, true},
		{"a quoted datastore never declared", "a.tf", `  datastore_id = "other"`, true},
		{"a quoted bridge", "a.tf", `    bridge = "vmbr9"`, true},
		{"a longer name is another name", "a.go", `"tank-images-old"`, false},
		{"read from the declaration", "a.tf", `  datastore_id = local.datastores[each.value].images`, false},
		{"a quoted datastore outside OpenTofu is the first check's", "a.yml", `datastore_id = "other"`, false},
	} {
		got := len(hypervisorFactsIn(c.rel, c.body, names, patterns)) > 0
		if got != c.want {
			t.Errorf("%s: found=%v, want %v", c.name, got, c.want)
		}
	}
}
