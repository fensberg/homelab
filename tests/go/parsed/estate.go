package parsed

import (
	"fmt"
	"homelab/details/deliveries"
	"strings"
	"sync"
	"testing"

	"homelab/contractor/config"
	"homelab/details/applications"
	"homelab/details/holds"
	"homelab/details/sizing"
	supplierslist "homelab/details/suppliers"
	"homelab/details/workorders"
	"homelab/tests/harness"
)

// The estate's own readers of what it declares. They are the programs' own
// code, handed the root by this package and by nothing else, so a guard asks
// the tree and never learns where it is.

// Applications is every application the tree declares.
func (r *Tree) Applications(t testing.TB) []applications.Application {
	t.Helper()
	apps, err := applications.Read(r.root)
	if err != nil {
		t.Fatal(err)
	}
	return apps
}

// Environments is the environments an application keeps settings for.
func (r *Tree) Environments(t testing.TB, a applications.Application) []string {
	t.Helper()
	envs, err := a.Environments(r.root)
	if err != nil {
		t.Fatal(err)
	}
	return envs
}

// Backups is every backup the tree's applications declare before a teardown.
func (r *Tree) Backups(t testing.TB) []applications.BeforeTeardown {
	t.Helper()
	b, err := applications.Backups(r.root)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// WorkOrders is every image the tree orders built.
func (r *Tree) WorkOrders(t testing.TB) []workorders.Order {
	t.Helper()
	orders, err := workorders.Read(r.root)
	if err != nil {
		t.Fatal(err)
	}
	return orders
}

// CoreSizing is what the core declares it reserves.
func (r *Tree) CoreSizing(t testing.TB) sizing.Declared {
	t.Helper()
	d, err := sizing.ReadCore(r.root)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// CoreHolds is what the core declares it holds that should outlive it.
func (r *Tree) CoreHolds(t testing.TB) []holds.Asset {
	t.Helper()
	a, err := holds.ReadCore(r.root)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// EstateRanges is the address plan's answer for sites: what each may use.
func (r *Tree) EstateRanges(t testing.TB, sites map[string]config.Site) map[string]config.Ranges {
	t.Helper()
	plan, err := config.EstateRanges(r.root, sites)
	if err != nil {
		t.Fatalf("asking the address plan: %v", err)
	}
	return plan
}

// fluxIndexes is each tree's index of the objects its Flux tree declares, by
// "<kind>/<name>", against the manifests declaring them.
var fluxIndexes sync.Map

// fluxIndex is every object the tree's Flux tree declares.
func (r *Tree) fluxIndex(t testing.TB) map[string][]string {
	t.Helper()
	if v, ok := fluxIndexes.Load(r.root); ok {
		return v.(map[string][]string)
	}
	index := map[string][]string{}
	for _, rel := range r.Files(t) {
		yaml := strings.HasSuffix(rel, ".yaml") || strings.HasSuffix(rel, ".yml")
		if !yaml || !strings.HasPrefix(rel, harness.FluxTree+"/") {
			continue
		}
		for _, doc := range r.YAMLDocuments(t, rel) {
			m, _ := doc.(map[string]any)
			kind, _ := m["kind"].(string)
			meta, _ := m["metadata"].(map[string]any)
			name, _ := meta["name"].(string)
			if kind != "" && name != "" {
				index[kind+"/"+name] = append(index[kind+"/"+name], rel)
			}
		}
	}
	fluxIndexes.Store(r.root, index)
	return index
}

// FluxObjectPath is the one manifest in the Flux tree declaring an object,
// found by what it declares, or why there is not one.
func (r *Tree) FluxObjectPath(t testing.TB, kind, name string) (string, error) {
	t.Helper()
	switch found := r.fluxIndex(t)[kind+"/"+name]; len(found) {
	case 1:
		return found[0], nil
	case 0:
		return "", fmt.Errorf("no manifest under %s declares a %s named %q. If it was renamed or removed, the check that reads it has nothing to read", harness.FluxTree, kind, name)
	default:
		return "", fmt.Errorf("%s %q is declared in %d manifests (%s), so there is no one file to read", kind, name, len(found), strings.Join(found, ", "))
	}
}

// FluxObject is that manifest, and every document it holds, decoded. The test
// fails if no manifest declares the object or several do.
func (r *Tree) FluxObject(t testing.TB, kind, name string) (string, []any) {
	t.Helper()
	path, err := r.FluxObjectPath(t, kind, name)
	if err != nil {
		t.Fatal(err)
	}
	return path, r.YAMLDocuments(t, path)
}

// suppliers is the suppliers list as the estate's own reader takes it.
func (r *Tree) suppliers(t testing.TB) []byte {
	t.Helper()
	return r.read(t, supplierslist.Path)
}

// Pools is the pools of pull-request runners the suppliers list declares.
func (r *Tree) Pools(t testing.TB) supplierslist.Pools {
	t.Helper()
	p, err := supplierslist.PoolsOf(r.suppliers(t))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Egress is what each job may reach, as the suppliers list declares it.
func (r *Tree) Egress(t testing.TB) []supplierslist.Egress {
	t.Helper()
	e, err := supplierslist.EgressOf(r.suppliers(t))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// Tools is every tool the suppliers list approves.
func (r *Tree) Tools(t testing.TB) []supplierslist.Tool {
	t.Helper()
	tools, err := supplierslist.Tools(r.suppliers(t))
	if err != nil {
		t.Fatal(err)
	}
	return tools
}

// Work is what kind of work each job on the estate's runners says it is, as
// the suppliers list declares it.
func (r *Tree) Work(t testing.TB) []harness.Work {
	t.Helper()
	w, err := harness.WorkOf(r.suppliers(t))
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// DeliveriesLock is every section of the deliveries lock, keyed "kind:name",
// as the security guard that refuses a wrong lock reads it.
func (r *Tree) DeliveriesLock(t testing.TB) map[string]deliveries.Section {
	t.Helper()
	sections, err := deliveries.Parse(string(r.read(t, deliveries.Path)))
	if err != nil {
		t.Fatalf("%s cannot be read: %v", deliveries.Path, err)
	}
	return sections
}
