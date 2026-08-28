package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTemplate(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tpl.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the fixture template: %v", err)
	}
	return path
}

func TestVaultReferencesFindsEveryReferenceWithItsConfigPath(t *testing.T) {
	path := writeTemplate(t, `{
	  "organization": { "name": "{{ op://homelab/organization/name }}" },
	  "sites": {
	    "site0": {
	      "octet": 10,
	      "hypervisor": {
	        "provider": "proxmox",
	        "token_secret": "{{ op://homelab/site0/hypervisor/token_secret }}"
	      }
	    }
	  }
	}`)

	refs, err := VaultReferences(path)
	if err != nil {
		t.Fatalf("VaultReferences: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("got %d references, want 2: %+v", len(refs), refs)
	}

	// Sorted by config path, so the report reads the same on every run - a map
	// walk would otherwise reorder it between invocations.
	if refs[0].ConfigPath != "organization.name" {
		t.Errorf("first reference is %q, want organization.name", refs[0].ConfigPath)
	}
	if refs[0].Ref != "op://homelab/organization/name" {
		t.Errorf("first ref is %q", refs[0].Ref)
	}
	if refs[1].ConfigPath != "sites.site0.hypervisor.token_secret" {
		t.Errorf("second reference is %q", refs[1].ConfigPath)
	}
}

// The literal values that sit beside the references - octets, provider names,
// counts - are not vault references and must not be probed.
func TestVaultReferencesIgnoresPlainValues(t *testing.T) {
	path := writeTemplate(t, `{
	  "a": "plain string",
	  "b": 10,
	  "c": true,
	  "d": "{{ op://homelab/item/field }}"
	}`)

	refs, err := VaultReferences(path)
	if err != nil {
		t.Fatalf("VaultReferences: %v", err)
	}
	if len(refs) != 1 || refs[0].ConfigPath != "d" {
		t.Fatalf("got %+v, want only the op:// leaf", refs)
	}
}

// An unwrapped op:// reference is the bug tests/go/repo's template test exists
// to catch. This function still has to see it: a preflight that skipped it
// would report a vault as complete while the run was about to hand the literal
// string "op://..." to a provider.
func TestVaultReferencesIncludesUnwrappedReferences(t *testing.T) {
	path := writeTemplate(t, `{ "a": "op://homelab/item/field" }`)

	refs, err := VaultReferences(path)
	if err != nil {
		t.Fatalf("VaultReferences: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("got %d references, want 1", len(refs))
	}
	if refs[0].Ref != "op://homelab/item/field" {
		t.Errorf("ref is %q", refs[0].Ref)
	}
}

// The reference is extracted out of the moustache, not returned with it -
// `op read "{{ op://... }}"` is not a thing.
func TestVaultReferencesStripsTheMoustache(t *testing.T) {
	path := writeTemplate(t, `{ "a": "{{ op://homelab/item/field }}" }`)

	refs, err := VaultReferences(path)
	if err != nil {
		t.Fatalf("VaultReferences: %v", err)
	}
	if got := refs[0].Ref; got != "op://homelab/item/field" {
		t.Errorf("ref is %q, want it free of braces and spaces", got)
	}
}

func TestVaultReferencesReportsAMissingTemplate(t *testing.T) {
	if _, err := VaultReferences(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatal("expected an error for a template that does not exist")
	}
}
