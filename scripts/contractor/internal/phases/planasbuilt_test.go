package phases

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"homelab/contractor/config"
	"homelab/details/asbuilt"
)

func savedRecord(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "record")
	res := &asbuilt.Result{Quiet: true,
		State:  map[string]any{"serial": json.Number("1"), "resources": []any{}},
		Config: map[string]any{}}
	meta := asbuilt.Meta{Site: "site0", Commit: "c0ffee1", Taken: time.Date(2026, 9, 27, 4, 0, 0, 0, time.UTC)}
	// A site's record is one per root, and the cluster's holds the outputs
	// the platform root is configured from.
	for _, root := range config.Roots {
		if root == config.ClusterRoot {
			var outputs map[string]any
			if err := json.Unmarshal([]byte(clusterOutputs), &outputs); err != nil {
				t.Fatal(err)
			}
			res.State["outputs"] = outputs
		} else {
			delete(res.State, "outputs")
		}
		if err := asbuilt.Write(filepath.Join(dir, root), res, meta); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// A change planned against a record is reported like any plan, and nothing
// about the record reaches the comment: the estate keeps the record current,
// so the operator reads the change and nothing about how it was found.
func TestAChangeIsPlannedAgainstTheRecordAndSaysWhich(t *testing.T) {
	ctx := recordContext(t)
	ctx.CommentOut = filepath.Join(t.TempDir(), "comment.md")
	for _, root := range ctx.Roots() {
		if err := os.MkdirAll(root.Dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// Each root's plan shows one thing, and the platform's is planned with
	// the cluster's access from the record.
	shows, handed := 0, false
	tofu := func(_ string, env []string, args ...string) ([]byte, []byte, error) {
		if args[0] != "show" {
			return nil, nil, nil
		}
		shows++
		if shows == 1 {
			return []byte(`{"format_version": "1.2", "resource_changes": [
			  {"address": "proxmox_vm.worker[3]", "mode": "managed", "type": "proxmox_vm", "name": "worker",
			   "change": {"actions": ["create"]}}]}`), nil, nil
		}
		handed = slices.Contains(env, "TF_VAR_kubeconfig=a kubeconfig")
		return []byte(`{"format_version": "1.2", "resource_changes": [
		  {"address": "kubernetes_namespace.games", "mode": "managed", "type": "kubernetes_namespace", "name": "games",
		   "change": {"actions": ["create"]}}]}`), nil, nil
	}
	if err := planAsBuilt(ctx, savedRecord(t), tofu); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(ctx.CommentOut)
	if !handed {
		t.Error("the platform root was planned without the cluster's access from the record")
	}
	for _, want := range []string{"## Plan — site0", "add", "proxmox_vm.worker[3]", "kubernetes_namespace.games"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the comment does not say %q:\n%s", want, body)
		}
	}
	if strings.Contains(string(body), "c0ffee1") || strings.Contains(string(body), "recorded") {
		t.Errorf("the comment talks about the record:\n%s", body)
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
