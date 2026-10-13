package phases

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"homelab/contractor/config"
	"homelab/contractor/internal/run"
	"homelab/contractor/steps"
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

// A pull request's plan can ask no hypervisor what it has. It is handed the
// facts the record holds from the last converge, and a record taken before
// there were any is planned with none, not refused.
func TestAPlanIsHandedTheHardwareTheRecordHolds(t *testing.T) {
	for name, c := range map[string]struct {
		recorded string
		want     bool
	}{
		"a record that holds the hardware":       {`{"nodes": {"node0": {"memory_bytes": 68719476736}}}`, true},
		"a record taken before it was ever read": {"", false},
	} {
		ctx := recordContext(t)
		for _, root := range ctx.Roots() {
			if err := os.MkdirAll(root.Dir, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		aSiteAsking(t, ctx, 3, 1)
		record := savedRecord(t)
		if c.recorded != "" {
			state, cfg, meta, err := asbuilt.Read(filepath.Join(record, config.ClusterRoot))
			if err != nil {
				t.Fatal(err)
			}
			var facts any
			if err := json.Unmarshal([]byte(c.recorded), &facts); err != nil {
				t.Fatal(err)
			}
			state["outputs"].(map[string]any)[hardwareInput] = map[string]any{"value": facts}
			if err := asbuilt.Write(filepath.Join(record, config.ClusterRoot), &asbuilt.Result{Quiet: true, State: state, Config: cfg}, meta); err != nil {
				t.Fatal(err)
			}
		}
		handed := false
		tofu := func(_ string, env []string, args ...string) ([]byte, []byte, error) {
			for _, e := range env {
				if strings.HasPrefix(e, "TF_VAR_"+hardwareInput+"=") && strings.Contains(e, `"memory_bytes":68719476736`) {
					handed = true
				}
			}
			if args[0] != "show" {
				return nil, nil, nil
			}
			return nothingPlanned(), nil, nil
		}
		t.Setenv("TF_VAR_"+hardwareInput, "")
		if err := planAsBuilt(ctx, record, tofu, noModulesOfItsOwn); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if handed != c.want {
			t.Errorf("%s: the plan was handed the recorded hardware = %v", name, handed)
		}
	}
}

// aSiteAsking is a config for a site of one hypervisor with the machines
// given: the contract corpus's valid config, with how many machines it has
// changed. What a site asks comes from that alone.
func aSiteAsking(t *testing.T, ctx *run.Context, controlPlanes, workers int, zones ...string) {
	t.Helper()
	root, err := repopath.Root()
	if err != nil {
		t.Fatal(err)
	}
	valid, err := os.ReadFile(filepath.Join(root, "modules", "infrastructure", "cluster", "tests", "fixtures", "valid.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	if err := json.Unmarshal(valid, &cfg); err != nil {
		t.Fatal(err)
	}
	site := cfg.Sites["site0"]
	site.ControlPlaneCount, site.WorkerCount = controlPlanes, workers
	site.DMZZones = map[string]config.DMZZone{}
	for _, zone := range zones {
		site.DMZZones[zone] = config.DMZZone{}
	}
	cfg.Sites["site0"] = site
	tpl, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ctx.ConfigTpl, tpl, 0o600); err != nil {
		t.Fatal(err)
	}
}

// recordHolding is a saved record whose cluster root holds the hardware
// given.
func recordHolding(t *testing.T, hardware string) string {
	t.Helper()
	record := savedRecord(t)
	state, cfg, meta, err := asbuilt.Read(filepath.Join(record, config.ClusterRoot))
	if err != nil {
		t.Fatal(err)
	}
	var facts any
	if err := json.Unmarshal([]byte(hardware), &facts); err != nil {
		t.Fatal(err)
	}
	state["outputs"].(map[string]any)[hardwareInput] = map[string]any{"value": facts}
	if err := asbuilt.Write(filepath.Join(record, config.ClusterRoot), &asbuilt.Result{Quiet: true, State: state, Config: cfg}, meta); err != nil {
		t.Fatal(err)
	}
	return record
}

// A change that asks its hypervisor for more memory than the record says it
// has is refused by the plan, in the converge's own words, and the comment
// leads with it: it is not left for the converge to find after the merge.
// A change that fits is planned and says nothing of it.
func TestAPlanRefusesAChangeItsHypervisorCannotHold(t *testing.T) {
	const gib = int64(1) << 30
	// A host of 64 GiB on which the site's four machines already stand:
	// three control planes and a worker, 22 GiB of the 64.
	standing := fmt.Sprintf(`{"nodes": {"node0": {"memory_bytes": %d, "machines": [
	  {"id": 10100, "memory_bytes": %d}, {"id": 10101, "memory_bytes": %d},
	  {"id": 10102, "memory_bytes": %d}, {"id": 10200, "memory_bytes": %d}]}}}`,
		64*gib, 4*gib, 4*gib, 4*gib, 10*gib)

	for name, c := range map[string]struct {
		workers int
		zones   []string
		refused bool
	}{
		"what stands":                   {1, nil, false},
		"one more worker, which fits":   {2, nil, false},
		"more than the host can give":   {6, nil, true},
		"a zone's machine that tips it": {4, []string{"example"}, true},
	} {
		ctx := recordContext(t)
		ctx.CommentOut = filepath.Join(t.TempDir(), "comment.md")
		for _, root := range ctx.Roots() {
			if err := os.MkdirAll(root.Dir, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		aSiteAsking(t, ctx, 3, c.workers, c.zones...)
		tofu := func(_ string, _ []string, args ...string) ([]byte, []byte, error) {
			if args[0] != "show" {
				return nil, nil, nil
			}
			return nothingPlanned(), nil, nil
		}
		t.Setenv("TF_VAR_"+hardwareInput, "")
		err := planAsBuilt(ctx, recordHolding(t, standing), tofu, noModulesOfItsOwn)
		comment, _ := os.ReadFile(ctx.CommentOut)
		if c.refused {
			if err == nil || !strings.Contains(err.Error(), "more memory than it can give") {
				t.Errorf("%s: was not refused: %v", name, err)
			}
			if !strings.Contains(string(comment), "REFUSED: the site asks its hypervisor for more memory than it can give") || !strings.Contains(string(comment), "OVER BY") {
				t.Errorf("%s: the comment does not lead with the refusal and its sum:\n%s", name, comment)
			}
			continue
		}
		if err != nil || strings.Contains(string(comment), "REFUSED") {
			t.Errorf("%s: a change that fits was refused: %v\n%s", name, err, comment)
		}
	}
}

// A record whose hardware cannot be read, or a config no site can be read
// from, stops the plan: a sum that could not be done is not a sum that
// came out even.
func TestAPlanThatCannotDoTheSumSaysSo(t *testing.T) {
	ctx := recordContext(t)
	for _, root := range ctx.Roots() {
		if err := os.MkdirAll(root.Dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	tofu := func(_ string, _ []string, args ...string) ([]byte, []byte, error) { return nothingPlanned(), nil, nil }

	aSiteAsking(t, ctx, 3, 1)
	if err := planAsBuilt(ctx, recordHolding(t, `{"nodes": "not hosts"}`), tofu, noModulesOfItsOwn); err == nil || !strings.Contains(err.Error(), "record's hardware cannot be read") {
		t.Errorf("hardware that cannot be read: %v", err)
	}
	if err := planAsBuilt(ctx, recordHolding(t, `{"nodes": {}}`), tofu, noModulesOfItsOwn); err == nil || !strings.Contains(err.Error(), "nothing read what that hypervisor has") {
		t.Errorf("a hypervisor the record does not hold: %v", err)
	}
	if err := os.WriteFile(ctx.ConfigTpl, []byte(`not a config`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := planAsBuilt(ctx, recordHolding(t, `{"nodes": {}}`), tofu, noModulesOfItsOwn); err == nil || !strings.Contains(err.Error(), "not JSON") {
		t.Errorf("a config that is not one: %v", err)
	}
	if err := os.WriteFile(ctx.ConfigTpl, []byte(`{"sites": "not sites"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := planAsBuilt(ctx, recordHolding(t, `{"nodes": {}}`), tofu, noModulesOfItsOwn); err == nil || !strings.Contains(err.Error(), "cannot be read as a site's") {
		t.Errorf("a config of the wrong shape: %v", err)
	}
}

// A plan that builds a template says, above itself, that the converge is
// the workstation's; one that builds none says nothing of it.
func TestAPlanThatBuildsATemplateSaysItIsForTheWorkstation(t *testing.T) {
	for name, c := range map[string]struct {
		summary string
		says    bool
	}{
		"a zone's template made":      {"  add     " + resourceOf(steps.ZoneTemplate) + `["<redacted>"]` + "\n  add     something.else\n", true},
		"the site's template rebuilt": {"  replace " + resourceOf(steps.Template) + `["<redacted>"]` + "\n", true},
		"a template only changed":     {"  change  " + resourceOf(steps.Template) + `["<redacted>"]  (name)` + "\n", false},
		"machines and no template":    {"  add     " + resourceOf(steps.ZoneMachines) + `["<redacted>"]` + "\n", false},
		"nothing at all":              {"", false},
	} {
		got := forTheWorkstation("site0", c.summary)
		if (got != "") != c.says {
			t.Errorf("%s: said %q", name, got)
		}
		if c.says && (!strings.Contains(got, "task converge SITE=site0") || !strings.Contains(got, "NEEDS THE WORKSTATION")) {
			t.Errorf("%s: the notice does not say what to run:\n%s", name, got)
		}
	}
	if resourceOf("module.somewhere.a_type.a_name") != "a_type.a_name" || resourceOf("alone") != "alone" {
		t.Error("a resource's own address was not read out of its full one")
	}
}
