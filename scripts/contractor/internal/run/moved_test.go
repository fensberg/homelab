package run

import (
	"homelab/contractor/repopath"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPendingMovesReadsBothEndpoints(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.tf", `
resource "cloudflare_r2_bucket" "database" {
  name = "x"
}

moved {
  from = cloudflare_r2_bucket.homelab
  to   = cloudflare_r2_bucket.database
}
`)
	// A second file, and an indexed address, because both occur here.
	write(t, dir, "b.tf", `
moved {
  from = proxmox_virtual_environment_vm.old["node0"]
  to   = proxmox_virtual_environment_vm.talos_cp["node0"]
}
`)
	// Not a .tf file, and must not be read.
	write(t, dir, "notes.md", "moved {\n  from = a.b\n  to = c.d\n}\n")

	got, err := PendingMoves(dir)
	if err != nil {
		t.Fatalf("PendingMoves: %v", err)
	}
	want := []string{
		`cloudflare_r2_bucket.database`,
		`cloudflare_r2_bucket.homelab`,
		`proxmox_virtual_environment_vm.old["node0"]`,
		`proxmox_virtual_environment_vm.talos_cp["node0"]`,
	}
	if len(got) != len(want) {
		t.Fatalf("got %d address(es) %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("address %d is %q, want %q", i, got[i], want[i])
		}
	}
}

func TestPendingMovesFindsNothingWhenNothingMoved(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.tf", `resource "terraform_data" "x" {}`)

	got, err := PendingMoves(dir)
	if err != nil {
		t.Fatalf("PendingMoves: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want none - a converge would then run a targeted refresh for no reason", got)
	}
}

// The parser is only worth having if it reads the estate's own source. A regex
// that silently matches nothing would leave SettleMoves a no-op and put the
// converge back exactly where it broke, with every test still green.
//
// Counted against a plain search for the keyword, so a `moved` block written in
// a shape this does not parse fails here rather than being skipped.
func TestPendingMovesFindsEveryMovedBlockInTheEstate(t *testing.T) {
	dir, err := repopath.Join("management", "cluster")
	if err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v - if the estate's source moved, this test has to move with it", dir, err)
	}
	blocks := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tf") {
			continue
		}
		body, readErr := os.ReadFile(filepath.Join(dir, e.Name()))
		if readErr != nil {
			t.Fatalf("reading %s: %v", e.Name(), readErr)
		}
		blocks += strings.Count(string(body), "\nmoved {")
	}

	got, err := PendingMoves(dir)
	if err != nil {
		t.Fatalf("PendingMoves: %v", err)
	}
	if want := blocks * 2; len(got) != want {
		t.Errorf(`the estate declares %d moved block(s) and PendingMoves returned %d address(es), want %d.

Every block names two endpoints and both have to be targeted, because OpenTofu
names both when it refuses a targeted plan. A block written in a shape this
parser does not read is a block that stops every converge at its first targeted
apply, silently, with this suite still green.

Addresses found: %v`, blocks, len(got), want, got)
	}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

// The flags are the decision, and each one is load-bearing: refresh-only so
// nothing can be created or destroyed, a target per endpoint so the refresh
// does not walk the whole configuration into data.talos_cluster_health, and
// -json so the output goes through the summary instead of into a public log.
func TestSettleMovesArgsCannotChangeAnythingAndStaysBounded(t *testing.T) {
	args := strings.Join(settleMovesArgs([]string{"a.b", "c.d"}), " ")

	for _, want := range []string{
		"-refresh-only",
		"-auto-approve",
		"-json",
		"-target=a.b",
		"-target=c.d",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("settleMovesArgs is missing %s: %s", want, args)
		}
	}
	if strings.Contains(args, "-refresh=false") {
		t.Error(`settleMovesArgs passes -refresh=false alongside -refresh-only.

OpenTofu refuses that combination outright ("OpenTofu would have nothing to
do"), so the settle would fail and every targeted apply after it would still be
refused.`)
	}
	if !strings.HasPrefix(args, "apply ") {
		t.Errorf("settleMovesArgs must be an apply - a plan records nothing, and recording the move is the point: %s", args)
	}
}
