package phases

import (
	"path/filepath"
	"strings"
	"testing"

	"homelab/steward/internal/run"
)

// Everything a run can leave behind has to be in this list, and a file missing
// from it fails silently: the run still reports success, and what is left is
// either a secret on disk or a workspace that no longer validates.
//
// .terraform/terraform.tfstate is the one that was missing. It is tofu's
// record of which backend is configured - not state - but it remembers that
// the last backend was encrypted Postgres, so the next `tofu init
// -backend=false` fails with "Unsupported state file format". That breaks
// `task validate` and `task test` on any machine that has completed a run,
// which is every machine that matters, and the pre-push hook runs both.
func TestSterilizeTargetsCoverEverythingARunLeavesBehind(t *testing.T) {
	ctx := run.NewContext(t.TempDir(), "site0")
	got := sterilizeTargets(ctx)

	for _, want := range []struct{ name, suffix string }{
		{"the rendered config", "management.rendered.json"},
		{"the generated Ansible inventory", "inventory.yml"},
		{"the site vars", "site.auto.yml"},
		{"the Postgres backend file", "backend_pg.tf"},
		{"the local state", "terraform.tfstate"},
		{"the state backup", "terraform.tfstate.backup"},
		{"tofu's backend record", filepath.Join(".terraform", "terraform.tfstate")},
	} {
		var found bool
		for _, g := range got {
			if strings.HasSuffix(g, want.suffix) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("sterilize does not remove %s (%s); a run would leave it behind and still report success", want.name, want.suffix)
		}
	}
}

// The backend record and the state file share a basename, which is exactly how
// one of them gets overlooked. Assert both paths are present, not just one.
func TestSterilizeRemovesBothStateFileAndBackendRecord(t *testing.T) {
	ctx := run.NewContext(t.TempDir(), "site0")
	got := sterilizeTargets(ctx)

	var localState, backendRecord bool
	for _, g := range got {
		if strings.HasSuffix(g, filepath.Join(".terraform", "terraform.tfstate")) {
			backendRecord = true
		} else if strings.HasSuffix(g, "terraform.tfstate") {
			localState = true
		}
	}
	if !localState {
		t.Error("the local state file is not sterilized")
	}
	if !backendRecord {
		t.Error("tofu's backend record is not sterilized; task validate breaks after any completed run")
	}
}
