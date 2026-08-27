package tfsource

import (
	"os"
	"path/filepath"
	"testing"
)

const sample = `
# octet_min = 999
locals {
  octet_min = 1
  octet_max = 95

  state_db_name  = "tofu_state"
  state_db_owner = "tofu"

  required_providers_by_concern = {
    hypervisor      = "proxmox"
    overlay_network = "tailscale"
    object_storage  = "cloudflare"
  }

  nested = {
    outer = "yes"
    inner = {
      deeper = "also"
    }
  }
}
`

func TestReadStripsCommentLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.tf")
	if err := os.WriteFile(path, []byte(sample), 0o600); err != nil {
		t.Fatalf("writing sample: %v", err)
	}

	src, err := Read(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The commented-out `octet_min = 999` must not survive, or a lookup
	// would find two assignments and report an ambiguity that isn't real.
	if got, err := Int(src, "octet_min"); err != nil || got != 1 {
		t.Errorf("octet_min = %d, %v; want 1, nil - a commented line leaked through", got, err)
	}
}

func TestInt(t *testing.T) {
	if got, err := Int(sample, "octet_max"); err != nil || got != 95 {
		t.Errorf("octet_max = %d, %v; want 95, nil", got, err)
	}
	if _, err := Int(sample, "state_db_name"); err == nil {
		t.Error("expected an error reading a quoted string as an integer")
	}
	if _, err := Int(sample, "does_not_exist"); err == nil {
		t.Error("expected an error for a missing assignment")
	}
}

func TestString(t *testing.T) {
	if got, err := String(sample, "state_db_owner"); err != nil || got != "tofu" {
		t.Errorf("state_db_owner = %q, %v; want \"tofu\", nil", got, err)
	}
	if _, err := String(sample, "octet_min"); err == nil {
		t.Error("expected an error reading a bare integer as a string literal")
	}
}

func TestMap(t *testing.T) {
	got, err := Map(sample, "required_providers_by_concern")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]string{
		"hypervisor":      "proxmox",
		"overlay_network": "tailscale",
		"object_storage":  "cloudflare",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %v", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

// A nested block must not end the scan at the inner closing brace, which is
// what a naive "find the next }" would do.
func TestMapWalksToTheMatchingBrace(t *testing.T) {
	got, err := Map(sample, "nested")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["outer"] != "yes" {
		t.Errorf("outer = %q, want \"yes\"", got["outer"])
	}
	if got["deeper"] != "also" {
		t.Errorf("deeper = %q, want \"also\" - the scan stopped at the inner brace", got["deeper"])
	}
}

func TestAmbiguousAssignmentIsAnError(t *testing.T) {
	src := "a = 1\na = 2\n"
	if _, err := Int(src, "a"); err == nil {
		t.Error("expected an error when a name is assigned twice")
	}
}
