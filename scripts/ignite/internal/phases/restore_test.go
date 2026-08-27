package phases

import (
	"strings"
	"testing"
)

// Restore is the other half of Backup, and until now it existed only as a
// recipe printed at the end of a run. A recipe is not a restore: it is three
// commands to retype correctly, under the one circumstance where nobody is
// calm - the estate is gone and the state that describes it went with it.
//
// The checks below are the ones that decide whether what came back is usable,
// and they run before anything is written anywhere.

func TestValidateRestoredState_AcceptsRealState(t *testing.T) {
	body := []byte(`{"version":4,"terraform_version":"1.12.6","serial":7,
	  "lineage":"6ee9a9ca-4e67-6e85-cf3f-1a11f8b1952c","resources":[{"type":"proxmox_virtual_environment_vm"},{"type":"cloudflare_r2_bucket"}]}`)
	got, err := validateRestoredState(body)
	if err != nil {
		t.Fatalf("validateRestoredState: %v", err)
	}
	if got.Serial != 7 || got.Resources != 2 {
		t.Errorf("got serial %d with %d resources, want 7 and 2", got.Serial, got.Resources)
	}
	if !strings.HasPrefix(got.Lineage, "6ee9a9ca") {
		t.Errorf("lineage not carried through: %q", got.Lineage)
	}
}

// age exits zero and writes nothing when handed an empty stream, so "the
// command succeeded" says nothing about whether state came back.
func TestValidateRestoredState_RejectsEmpty(t *testing.T) {
	if _, err := validateRestoredState(nil); err == nil {
		t.Error("expected an error for an empty restore")
	}
}

// The failure that actually happens: the identity is wrong, or the object was
// never state to begin with, and what lands is a few hundred bytes of
// something else. Writing that over a workspace is worse than failing.
func TestValidateRestoredState_RejectsNonState(t *testing.T) {
	if _, err := validateRestoredState([]byte(`{"hello":"world"}`)); err == nil {
		t.Error("expected an error for JSON that is not OpenTofu state")
	}
	if _, err := validateRestoredState([]byte("age-encryption.org/v1")); err == nil {
		t.Error("expected an error for ciphertext that was never decrypted")
	}
}

// State with no resources in it is valid JSON and completely useless: it is
// what an empty workspace produces, and pushing it over a real backend is how
// a restore becomes a deletion.
func TestValidateRestoredState_RejectsStateWithNoResources(t *testing.T) {
	_, err := validateRestoredState([]byte(`{"version":4,"serial":1,"lineage":"x","resources":[]}`))
	if err == nil {
		t.Fatal("expected an error for state describing nothing")
	}
	if !strings.Contains(err.Error(), "no resources") {
		t.Errorf("the error should say what is wrong, got: %v", err)
	}
}

func TestBackupObjectKey_DefaultsToLatest(t *testing.T) {
	if got := backupObjectKey("my-bucket"); got != "R2:my-bucket/management-cluster/latest.tfstate.age" {
		t.Errorf("got %q", got)
	}
}
