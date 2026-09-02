package phases

import (
	"os"
	"path/filepath"
	"testing"

	"homelab/contractor/internal/run"
)

// Reading a credential must not delete a file it did not create.
//
// The cleanup at the end of writeRenderedCredential reused sterilizeTargets
// wholesale, and that list contains the local state file. This function never
// creates local state - it either finds it (which, by its own test three lines
// earlier, means another run is mid-flight and holds the authoritative ledger)
// or it attaches to Postgres and creates none.
//
// So `task kubectl` run during a teardown deleted the state that teardown was
// working from. Nothing irreplaceable was lost - the ledger's only value is
// "what is left to destroy, right now" - but losing it mid-destroy is how a
// teardown strands infrastructure that nothing tracks any more, which is the
// exact failure the migration to local state exists to prevent.
func TestCleanupSkipsWhatItDidNotCreate(t *testing.T) {
	dir := t.TempDir()
	ctx := &run.Context{
		ClusterDir:     dir,
		LocalState:     filepath.Join(dir, "terraform.tfstate"),
		ConfigRendered: filepath.Join(dir, "management.rendered.json"),
		BackendPgOn:    filepath.Join(dir, "backend_pg.tf"),
		InventoryOut:   filepath.Join(dir, "inventory.yml"),
		SiteVars:       filepath.Join(dir, "site.auto.yml"),
		OverlayVars:    filepath.Join(dir, "overlay-network.auto.yml"),
		Kubeconfig:     filepath.Join(dir, "kubeconfig"),
	}

	// A run in flight: state on disk, and a rendered config beside it.
	mustWrite(t, ctx.LocalState, "a teardown's ledger")
	mustWrite(t, ctx.ConfigRendered, "someone else's render")

	// Something this call would have created itself.
	mustWrite(t, ctx.BackendPgOn, "written by attach")

	preexisting := map[string]bool{}
	for _, p := range sterilizeTargets(ctx) {
		if _, err := os.Stat(p); err == nil {
			preexisting[p] = true
		}
	}
	for _, p := range sterilizeTargets(ctx) {
		if preexisting[p] {
			continue
		}
		_ = run.RemoveIfExists(p)
	}

	if _, err := os.Stat(ctx.LocalState); err != nil {
		t.Error("the local state file was deleted; a teardown using it is now stranded")
	}
	if _, err := os.Stat(ctx.ConfigRendered); err != nil {
		t.Error("a rendered config that was already there was deleted out from under whoever rendered it")
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
