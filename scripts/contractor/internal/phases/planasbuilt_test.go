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
	"homelab/details/platform"
	"homelab/details/repopath"
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
	if err := planAsBuilt(ctx, savedRecord(t), tofu, noModulesOfItsOwn); err != nil {
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
	if err := planAsBuilt(recordContext(t), "", nil, noModulesOfItsOwn); err == nil || !strings.Contains(err.Error(), "-record") {
		t.Errorf("got %v", err)
	}
	if err := planAsBuilt(recordContext(t), t.TempDir(), nil, noModulesOfItsOwn); err == nil {
		t.Error("an empty directory was planned against as a record")
	}
}

// noModulesOfItsOwn stands for a checkout whose modules were placed, at a
// path the stand-in tofu can recognise.
func noModulesOfItsOwn(string) (string, error) { return "../../the-checkouts-own", nil }

// nothingPlanned is a plan that changes nothing, as tofu shows one.
func nothingPlanned() []byte { return []byte(asbuilt.NothingPlanned) }

const viaTheCheckout = "TF_VAR_unreleased=../../the-checkouts-own"

// A change to a module changes nothing a site runs until the site moves to a
// release that holds it, so planned as the site runs it says nothing. The
// reader is shown what the site would change once it runs the modules as the
// change has them, on the pull request where the change is decided.
func TestAChangeToAModuleShowsWhatASiteWouldChangeOnceItRunsIt(t *testing.T) {
	ctx := recordContext(t)
	ctx.CommentOut = filepath.Join(t.TempDir(), "comment.md")
	for _, root := range ctx.Roots() {
		if err := os.MkdirAll(root.Dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// As released, nothing changes. With the checkout's own modules, the
	// platform root gains a namespace.
	tofu := func(_ string, env []string, args ...string) ([]byte, []byte, error) {
		if args[0] != "show" {
			return nil, nil, nil
		}
		if slices.Contains(env, viaTheCheckout) && slices.Contains(env, "TF_VAR_kubeconfig=a kubeconfig") {
			return []byte(`{"format_version": "1.2", "resource_changes": [
			  {"address": "kubernetes_namespace.storage", "mode": "managed", "type": "kubernetes_namespace", "name": "storage",
			   "change": {"actions": ["create"]}}]}`), nil, nil
		}
		return nothingPlanned(), nil, nil
	}
	if err := planAsBuilt(ctx, savedRecord(t), tofu, noModulesOfItsOwn); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(ctx.CommentOut)
	said := string(body)
	first, second, found := strings.Cut(said, "Once it runs the modules as they are in this change")
	if !found {
		t.Fatalf("the comment does not say what the site would change once it runs this change's modules:\n%s", said)
	}
	if strings.Contains(first, "kubernetes_namespace.storage") {
		t.Errorf("what merging does now is reported as including a change only the new modules make:\n%s", said)
	}
	if !strings.Contains(second, "kubernetes_namespace.storage") {
		t.Errorf("the change the new modules make is not shown:\n%s", said)
	}
	if strings.Count(said, commentMarker("site0")) != 1 {
		t.Errorf("the comment carries its marker other than once, so the next plan would not replace it:\n%s", said)
	}
}

// A change that touches no module plans the same both ways, and is reported
// once.
func TestAChangeThatTouchesNoModuleIsReportedOnce(t *testing.T) {
	ctx := recordContext(t)
	ctx.CommentOut = filepath.Join(t.TempDir(), "comment.md")
	for _, root := range ctx.Roots() {
		if err := os.MkdirAll(root.Dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	tofu := func(_ string, _ []string, args ...string) ([]byte, []byte, error) {
		if args[0] != "show" {
			return nil, nil, nil
		}
		return []byte(`{"format_version": "1.2", "resource_changes": [
		  {"address": "proxmox_vm.worker[3]", "mode": "managed", "type": "proxmox_vm", "name": "worker",
		   "change": {"actions": ["create"]}}]}`), nil, nil
	}
	if err := planAsBuilt(ctx, savedRecord(t), tofu, noModulesOfItsOwn); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(ctx.CommentOut)
	// Once for each root, and not twice over.
	if strings.Contains(string(body), "Once it runs") || strings.Count(string(body), "proxmox_vm.worker[3]") != len(ctx.Roots()) {
		t.Errorf("a change that plans the same with either set of modules is reported twice:\n%s", body)
	}
}

// What cannot be planned with the change's own modules is a refusal, not a
// plan with half of it missing: a module the change broke is exactly what
// this second pass is there to find.
func TestAChangeWhoseModulesCannotBePlacedOrPlannedIsRefused(t *testing.T) {
	ctx := recordContext(t)
	for _, root := range ctx.Roots() {
		if err := os.MkdirAll(root.Dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	quiet := func(_ string, _ []string, args ...string) ([]byte, []byte, error) {
		if args[0] != "show" {
			return nil, nil, nil
		}
		return nothingPlanned(), nil, nil
	}
	unplaceable := func(string) (string, error) { return "", os.ErrNotExist }
	if err := planAsBuilt(ctx, savedRecord(t), quiet, unplaceable); err == nil {
		t.Error("a checkout whose modules could not be placed was planned as though it had none")
	}
	broken := func(_ string, env []string, args ...string) ([]byte, []byte, error) {
		if slices.Contains(env, viaTheCheckout) && args[0] == "plan" {
			return nil, []byte("Error: Unsupported argument"), os.ErrInvalid
		}
		return quiet("", env, args...)
	}
	err := planAsBuilt(ctx, savedRecord(t), broken, noModulesOfItsOwn)
	if err == nil || !strings.Contains(err.Error(), "with the modules as they are in this change") {
		t.Errorf("a module that does not plan was not refused as the change's own: %v", err)
	}
}

// The real placing, against this repository: what a root is told to read is
// a tree holding the modules a release would.
func TestTheCheckoutsOwnModulesArePlacedWhereARootReadsThem(t *testing.T) {
	repo := repopath.RootOrFail(t)
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(repo, platform.Unreleased)) })
	named, err := placeUnreleased(repo)
	if err != nil {
		t.Fatal(err)
	}
	if named != "../../"+platform.Unreleased {
		t.Errorf("a root two directories down is told to read %s", named)
	}
	if _, err := os.Stat(filepath.Join(repo, platform.Unreleased, "modules", "infrastructure", "platform")); err != nil {
		t.Errorf("the placed tree does not hold the platform module: %v", err)
	}
}
