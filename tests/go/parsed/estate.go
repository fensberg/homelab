package parsed

import (
	"testing"

	"homelab/details/applications"
	"homelab/details/holds"
	"homelab/details/sizing"
	"homelab/details/workorders"
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
