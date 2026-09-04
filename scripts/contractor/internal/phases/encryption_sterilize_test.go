package phases

import "testing"

// The cleanup must not need the vault to delete files.
//
// integration-tests.yml ends with a step that runs `-phase sterilize` with
// `if: always()`, carrying SITE and deliberately not OP_SERVICE_ACCOUNT_TOKEN -
// because a self-hosted runner is long-lived and a rendered config left on it
// stays there, and removing files should not need vault access.
//
// On 2026-09-04 that step failed with "still not signed in to 1Password",
// because every invocation established state encryption before dispatching a
// phase. The safety net could not run in the conditions it exists for: a run
// that gets past Render and then fails leaves secrets on disk.
func TestSterilizeDoesNotNeedTheVault(t *testing.T) {
	if NeedsStateEncryption("sterilize") {
		t.Error("sterilize would demand a vault session; the cleanup step has none, by design")
	}
}

// Every other phase still does, and that is not incidental.
//
// State is encrypted at rest, so a tofu invocation without TF_ENCRYPTION
// cannot read it. Establishing it per phase would leave `-from cluster` and
// the teardown unable to reach the state they exist to operate on.
func TestEveryOtherPhaseStillEstablishesIt(t *testing.T) {
	var checked int
	for _, p := range AllPhases {
		if p == "sterilize" {
			continue
		}
		checked++
		if !NeedsStateEncryption(p) {
			t.Errorf("phase %q would run without state encryption, so tofu could not read the state", p)
		}
	}
	if checked == 0 {
		t.Fatal("no phases checked, so this test proves nothing")
	}
	// A whole-sequence run is not a single phase, and must still establish it.
	if !NeedsStateEncryption("") {
		t.Error("a full run would skip state encryption")
	}
}
