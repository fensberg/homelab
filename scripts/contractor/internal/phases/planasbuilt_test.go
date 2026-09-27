package phases

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homelab/details/asbuilt"
)

func savedRecord(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "record")
	res := &asbuilt.Result{Quiet: true,
		State:  map[string]any{"serial": json.Number("1"), "resources": []any{}},
		Config: map[string]any{}}
	meta := asbuilt.Meta{Site: "site0", Commit: "c0ffee1", Taken: time.Date(2026, 9, 27, 4, 0, 0, 0, time.UTC)}
	if err := asbuilt.Write(dir, res, meta); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A change planned against a record is reported like any plan, and its
// comment says which record it was compared with.
func TestAChangeIsPlannedAgainstTheRecordAndSaysWhich(t *testing.T) {
	ctx := recordContext(t)
	ctx.CommentOut = filepath.Join(t.TempDir(), "comment.md")
	if err := os.MkdirAll(ctx.ClusterDir, 0o700); err != nil {
		t.Fatal(err)
	}
	tofu := func(_ string, _ []string, args ...string) ([]byte, []byte, error) {
		if args[0] == "show" {
			return []byte(`{"format_version": "1.2", "resource_changes": [
			  {"address": "proxmox_vm.worker[3]", "mode": "managed", "type": "proxmox_vm", "name": "worker",
			   "change": {"actions": ["create"]}}]}`), nil, nil
		}
		return nil, nil, nil
	}
	if err := planAsBuilt(ctx, savedRecord(t), tofu); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(ctx.CommentOut)
	for _, want := range []string{"## Plan — site0", "after `c0ffee1` converged, on 2026-09-27 04:00 UTC", "add", "proxmox_vm.worker[3]"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the comment does not say %q:\n%s", want, body)
		}
	}
	if !strings.HasPrefix(string(body), commentMarker("site0")) {
		t.Error("the comment lost its marker, so the next plan would add another rather than replace it")
	}
}

func TestPlanningAsBuiltNeedsARecord(t *testing.T) {
	if err := planAsBuilt(recordContext(t), "", nil); err == nil || !strings.Contains(err.Error(), "-record") {
		t.Errorf("got %v", err)
	}
	if err := planAsBuilt(recordContext(t), t.TempDir(), nil); err == nil {
		t.Error("an empty directory was planned against as a record")
	}
}
