package asbuilt

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func publishable() *Result {
	return &Result{
		Quiet:  true,
		State:  map[string]any{"serial": json.Number("3"), "resources": []any{}},
		Config: map[string]any{"sites": map[string]any{"site0": map[string]any{"name": "rstandin"}}},
	}
}

// A record round-trips through Write and Read, and an unpublishable one is
// never written: it would make every plan against it wrong, or carry a real
// value off the machine.
func TestARecordIsWrittenOnlyWhenPublishableAndReadsBack(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "record")
	meta := Meta{Site: "site0", Commit: "abc1234", Taken: time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)}

	noisy := publishable()
	noisy.Quiet = false
	if err := Write(dir, noisy, meta); err == nil {
		t.Fatal("a record that is not quiet was written")
	}
	leaky := publishable()
	leaky.Before = []Finding{{Where: "a.b.c", Source: SourceVault}}
	if err := Write(dir, leaky, meta); err == nil {
		t.Fatal("a record holding a real value was written")
	}
	if _, err := os.Stat(dir); err == nil {
		t.Fatal("an unpublishable record left a directory behind")
	}

	if err := Write(dir, publishable(), meta); err != nil {
		t.Fatal(err)
	}
	state, config, got, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != meta {
		t.Errorf("meta came back as %+v", got)
	}
	if state["serial"] != json.Number("3") {
		t.Errorf("the state came back as %v", state)
	}
	if config["sites"].(map[string]any)["site0"].(map[string]any)["name"] != "rstandin" {
		t.Errorf("the config came back as %v", config)
	}
}

// A record missing any of its parts is not read as a smaller record.
func TestAnIncompleteRecordIsRefused(t *testing.T) {
	for _, missing := range []string{stateFile, configFile, metaFile} {
		dir := t.TempDir()
		if err := Write(dir, publishable(), Meta{Site: "site0"}); err != nil {
			t.Fatal(err)
		}
		_ = os.Remove(filepath.Join(dir, missing))
		if _, _, _, err := Read(dir); err == nil {
			t.Errorf("a record without %s was read", missing)
		}
	}
	dir := t.TempDir()
	_ = Write(dir, publishable(), Meta{Site: "site0"})
	_ = os.WriteFile(filepath.Join(dir, metaFile), []byte("not json"), 0o600)
	if _, _, _, err := Read(dir); err == nil {
		t.Error("a record whose metadata is not JSON was read")
	}
	_ = os.WriteFile(filepath.Join(dir, stateFile), []byte("not json"), 0o600)
	if _, _, _, err := Read(dir); err == nil {
		t.Error("a record whose state is not JSON was read")
	}
}

// The change's literals win, because they are what it proposes; the record's
// stand-ins fill the vault references, because they are what the estate was
// built with; a vault field the change adds gets a stand-in of the right
// shape; a key the change removes goes.
func TestReconcileTakesTheChangesLiteralsAndTheRecordsStandIns(t *testing.T) {
	f := mustFingerprinter(t, 1)
	tpl := mustDecode(t, `{"sites": {"site0": {
	  "name": "{{ op://site0-shared/identity/name }}",
	  "worker_count": 4,
	  "nodes": [{"ip": "{{ op://site0/node0/ip }}"}, {"ip": "{{ op://site0/node1/ip }}"}],
	  "tags": ["a", "b"]
	}}}`)
	recorded := mustDecode(t, `{"sites": {"site0": {
	  "name": "rrecorded",
	  "worker_count": 3,
	  "nodes": [{"ip": "198.18.1.2"}],
	  "removed": "gone"
	}}}`)
	got := Reconcile(f, "", tpl, recorded).(map[string]any)["sites"].(map[string]any)["site0"].(map[string]any)
	if got["name"] != "rrecorded" {
		t.Errorf("a vault value did not take the record's stand-in: %v", got["name"])
	}
	if got["worker_count"] != json.Number("4") {
		t.Errorf("the change's literal lost to the record's: %v", got["worker_count"])
	}
	nodes := got["nodes"].([]any)
	if nodes[0].(map[string]any)["ip"] != "198.18.1.2" {
		t.Errorf("a recorded address was not kept: %v", nodes[0])
	}
	added := nodes[1].(map[string]any)["ip"].(string)
	if !strings.HasPrefix(added, "198.1") || strings.Contains(added, "op://") {
		t.Errorf("a vault field the change adds was not given an address-shaped stand-in: %q", added)
	}
	if _, ok := got["removed"]; ok {
		t.Error("a key the change removed survived from the record")
	}
	if !slices.Equal(got["tags"].([]any), []any{"a", "b"}) {
		t.Errorf("a list of literals changed: %v", got["tags"])
	}
}

// The plan runs against the record with the change's own files, offline, and
// holds no credential.
func TestAChangeIsPlannedAgainstTheRecord(t *testing.T) {
	in, f := takeFixture(t)
	record := filepath.Join(t.TempDir(), "record")
	if err := Write(record, publishable(), Meta{Site: "site0", Commit: "abc1234"}); err != nil {
		t.Fatal(err)
	}
	f.offlinePlans = []string{quietOfflinePlan}
	f.work = in.Work
	var inits [][]string
	run := func(dir string, env []string, args ...string) ([]byte, []byte, error) {
		if dir != filepath.Join(in.Work, "plan") {
			t.Errorf("tofu ran outside the plan's own directory: %s", dir)
		}
		if !slices.Contains(env, "TF_VAR_offline=true") || slices.ContainsFunc(env, func(s string) bool { return strings.HasPrefix(s, "OP_") }) {
			t.Errorf("the plan did not run in the offline environment: %v", args)
		}
		if args[0] == "init" {
			inits = append(inits, args)
		}
		if args[0] == "show" {
			return []byte(quietOfflinePlan), nil, nil
		}
		return nil, nil, nil
	}
	t.Setenv("OP_EXAMPLE_TOKEN", "must-not-reach-tofu")
	plan, meta, err := PlanAgainst(PlanInputs{
		Root: in.Root, Work: in.Work, Record: record, Site: "site0",
		Template:  []byte(`{"sites": {"site0": {"name": "{{ op://site0-shared/identity/name }}"}}}`),
		PluginDir: "/cache",
	}, run)
	if err != nil {
		t.Fatal(err)
	}
	if string(plan) != quietOfflinePlan || meta.Commit != "abc1234" {
		t.Errorf("got plan %s, meta %+v", plan, meta)
	}
	if len(inits) != 1 || !slices.Contains(inits[0], "-plugin-dir=/cache") {
		t.Errorf("init was %v", inits)
	}
	config, _ := os.ReadFile(filepath.Join(in.Work, "plan", configFile))
	if !strings.Contains(string(config), "rstandin") {
		t.Errorf("the plan's config did not take the record's stand-in:\n%s", config)
	}
	if _, err := os.Stat(filepath.Join(in.Work, "plan", "backend_pg.tf")); err == nil {
		t.Error("the plan carries the real backend")
	}
}

// A record of another site, a template that is not JSON, and every tofu
// failure are errors that say which step failed.
func TestPlanningAgainstARecordFailsLoudly(t *testing.T) {
	in, _ := takeFixture(t)
	record := filepath.Join(t.TempDir(), "record")
	_ = Write(record, publishable(), Meta{Site: "site0"})
	base := PlanInputs{Root: in.Root, Work: in.Work, Record: record, Site: "site0", Template: []byte(`{}`)}
	ok := func(string, []string, ...string) ([]byte, []byte, error) { return nil, nil, nil }

	other := base
	other.Site = "site1"
	if _, _, err := PlanAgainst(other, ok); err == nil || !strings.Contains(err.Error(), "not site1") {
		t.Errorf("a record of another site: %v", err)
	}
	bad := base
	bad.Template = []byte("not json")
	if _, _, err := PlanAgainst(bad, ok); err == nil {
		t.Error("a template that is not JSON was accepted")
	}
	missing := base
	missing.Record = t.TempDir()
	if _, _, err := PlanAgainst(missing, ok); err == nil {
		t.Error("an empty directory was accepted as a record")
	}
	for _, step := range []string{"init", "plan", "show"} {
		failing := func(_ string, _ []string, args ...string) ([]byte, []byte, error) {
			if args[0] == step {
				return nil, []byte("Error: " + step + " broke"), errors.New("exit 1")
			}
			return nil, nil, nil
		}
		if _, _, err := PlanAgainst(base, failing); err == nil {
			t.Errorf("a failing tofu %s was not an error", step)
		}
	}
}
