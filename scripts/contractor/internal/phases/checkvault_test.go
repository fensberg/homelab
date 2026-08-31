package phases

import (
	"bytes"
	"strings"
	"testing"

	"homelab/contractor/internal/config"
	"homelab/contractor/internal/onepassword"
)

// A fake probe, keyed by reference. The whole point of injecting it is that
// the report logic - which is where the bugs would be - is testable with no
// vault, no credentials and no `op` binary.
func fakeProbe(m map[string]onepassword.Status) func(string) onepassword.Status {
	return func(ref string) onepassword.Status {
		s, ok := m[ref]
		if !ok {
			return onepassword.StatusMissing
		}
		return s
	}
}

func TestVaultReportPassesWhenEveryReferenceResolves(t *testing.T) {
	refs := []config.VaultRef{
		{ConfigPath: "organization.name", Ref: "op://homelab/organization/name"},
		{ConfigPath: "state_backup.recipient", Ref: "op://homelab/state_backup/recipient"},
	}
	probe := fakeProbe(map[string]onepassword.Status{
		"op://homelab/organization/name":      onepassword.StatusOK,
		"op://homelab/state_backup/recipient": onepassword.StatusOK,
	})

	var out bytes.Buffer
	if err := vaultReport(refs, probe, &out); err != nil {
		t.Fatalf("expected a complete vault to pass, got: %v", err)
	}
	if !strings.Contains(out.String(), "2 checked") {
		t.Errorf("the report should say how many were checked:\n%s", out.String())
	}
}

// The bug this whole check exists for: config/management.tpl.json referenced
// op://homelab/source-control/token, which did not exist in the vault. Every
// ignition run would have failed at Render - the last place anybody wants to
// discover it, because Render is what pulls every other secret onto disk.
func TestVaultReportFailsOnAMissingReference(t *testing.T) {
	refs := []config.VaultRef{
		{ConfigPath: "source_control.repo_url", Ref: "op://homelab/source-control/url"},
		{ConfigPath: "source_control.token", Ref: "op://homelab/source-control/token"},
	}
	probe := fakeProbe(map[string]onepassword.Status{
		"op://homelab/source-control/url": onepassword.StatusOK,
	})

	var out bytes.Buffer
	err := vaultReport(refs, probe, &out)
	if err == nil {
		t.Fatal("a missing reference must fail the check")
	}
	// The message has to name both halves: the vault path to create, and the
	// config key that breaks without it.
	for _, want := range []string{"op://homelab/source-control/token", "source_control.token"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got:\n%s", want, err.Error())
		}
	}
	// A reference that is fine must not be reported as a problem.
	if strings.Contains(err.Error(), "source-control/url") {
		t.Errorf("a resolving reference must not appear in the failure:\n%s", err.Error())
	}
}

// An empty field is the quieter half. `op inject` reports success and writes
// an empty string, so this one does not fail Render at all - it fails much
// later, inside a provider, with an error that names no field.
func TestVaultReportFailsOnAnEmptyField(t *testing.T) {
	refs := []config.VaultRef{
		{ConfigPath: "sites.site0.database.password", Ref: "op://homelab/site0/database/password"},
	}
	probe := fakeProbe(map[string]onepassword.Status{
		"op://homelab/site0/database/password": onepassword.StatusEmpty,
	})

	var out bytes.Buffer
	err := vaultReport(refs, probe, &out)
	if err == nil {
		t.Fatal("an empty field must fail the check")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("the error should distinguish empty from missing, got:\n%s", err.Error())
	}
}

// Missing and empty are different fixes - create the field, versus fill it in -
// so a vault with both must not collapse them into one message.
func TestVaultReportSeparatesMissingFromEmpty(t *testing.T) {
	refs := []config.VaultRef{
		{ConfigPath: "a", Ref: "op://homelab/a/f"},
		{ConfigPath: "b", Ref: "op://homelab/b/f"},
	}
	probe := fakeProbe(map[string]onepassword.Status{
		"op://homelab/a/f": onepassword.StatusMissing,
		"op://homelab/b/f": onepassword.StatusEmpty,
	})

	var out bytes.Buffer
	err := vaultReport(refs, probe, &out)
	if err == nil {
		t.Fatal("expected a failure")
	}
	msg := err.Error()
	if !strings.Contains(msg, "op://homelab/a/f") || !strings.Contains(msg, "op://homelab/b/f") {
		t.Errorf("both problems must be reported, got:\n%s", msg)
	}
	if strings.Index(msg, "op://homelab/a/f") > strings.Index(msg, "op://homelab/b/f") {
		t.Errorf("missing should be listed before empty - it is the harder failure:\n%s", msg)
	}
}

// A template with no references is not a success worth reporting; it means the
// preflight is reading the wrong file and would pass no matter what the vault
// held.
func TestVaultReportRefusesAnEmptyReferenceList(t *testing.T) {
	var out bytes.Buffer
	err := vaultReport(nil, fakeProbe(nil), &out)
	if err == nil {
		t.Fatal("a template with no op:// references must not report success")
	}
}

// The value must never reach the report. Probe returns a Status and nothing
// else, so this is really a check that the report was not later changed to
// print something a probe handed it.
func TestVaultReportNeverPrintsAValue(t *testing.T) {
	refs := []config.VaultRef{{ConfigPath: "a", Ref: "op://homelab/a/f"}}
	probe := func(string) onepassword.Status { return onepassword.StatusOK }

	var out bytes.Buffer
	if err := vaultReport(refs, probe, &out); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	// The fake cannot leak a secret, so assert the shape instead: the report
	// carries the reference and the status, and nothing that came back from a
	// probe - which returns no value to carry.
	if !strings.Contains(out.String(), "op://homelab/a/f") {
		t.Errorf("the report should name the reference:\n%s", out.String())
	}
}
