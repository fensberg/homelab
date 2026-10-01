package asbuilt

import (
	"crypto/tls"
	"encoding/base64"
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
		PluginDir: "/cache", Sequence: []PlanStep{{Label: "everything"}},
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
	base := PlanInputs{Root: in.Root, Work: in.Work, Record: record, Site: "site0", Template: []byte(`{}`),
		Sequence: []PlanStep{{Label: "everything"}}}
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

// A change that keys a resource by a value from the vault is refused, with
// the resource and the config field named - whichever vault value it is, and
// whether the resource is already built or the change adds it.
func TestAChangeThatKeysAResourceByAVaultValueIsRefused(t *testing.T) {
	in, _ := takeFixture(t)
	record := filepath.Join(t.TempDir(), "record")
	if err := Write(record, publishable(), Meta{Site: "site0"}); err != nil {
		t.Fatal(err)
	}
	keyed := `{"format_version": "1.2", "resource_changes": [
	  {"address": "proxmox_vm.template[\"rstandin\"]", "mode": "managed", "type": "proxmox_vm", "name": "template",
	   "index": "rstandin", "change": {"actions": ["create"]}}]}`
	run := func(_ string, _ []string, args ...string) ([]byte, []byte, error) {
		if args[0] == "show" {
			return []byte(keyed), nil, nil
		}
		return nil, nil, nil
	}
	_, _, err := PlanAgainst(PlanInputs{
		Root: in.Root, Work: in.Work, Record: record, Site: "site0",
		Template: []byte(`{"sites": {"site0": {"name": "{{ op://site0-shared/identity/name }}"}}}`),
		Sequence: []PlanStep{{Label: "everything"}},
	}, run)
	var refused *VaultKeyError
	if !errors.As(err, &refused) {
		t.Fatalf("got %v", err)
	}
	if len(refused.Keyed) != 1 || refused.Keyed[0] != "proxmox_vm.template is keyed by sites.site0.name" {
		t.Errorf("refused %v", refused.Keyed)
	}
	if !strings.Contains(err.Error(), "proxmox_vm.template") || !strings.Contains(err.Error(), "sites.site0.name") {
		t.Errorf("the refusal does not say what and which field: %v", err)
	}
}

func TestKeyedByAVaultValueSeesEveryVaultFieldAndOnlyThose(t *testing.T) {
	tpl := mustDecode(t, `{
	  "organization": {"name": "{{ op://estate/organization/name }}", "label": "public"},
	  "tunnel": {"vault_provider": "{{ op://estate/tunnel/provider }}", "token": "{{ op://estate/tunnel/token }}"},
	  "sites": {"site0": {"octet": 10, "short": "{{ op://site0/short }}",
	    "nodes": [{"hostname": "{{ op://site0/node0/hostname }}", "ip": "{{ op://site0/node0/ip }}"}]}}}`)
	config := mustDecode(t, `{
	  "organization": {"name": "rorgname0001", "label": "public"},
	  "tunnel": {"vault_provider": "cloudflare", "token": "rtoken000001"},
	  "sites": {"site0": {"octet": 10, "short": "ab",
	    "nodes": [{"hostname": "rhostname001", "ip": "198.18.4.5"}]}}}`)
	change := func(typ, name, index string) string {
		addr := typ + "." + name
		if index != "" {
			addr += `[\"` + index + `\"]`
		}
		return `{"address": "` + addr + `", "type": "` + typ + `", "name": "` + name + `", "index": "` + index + `"}`
	}
	plan := `{"resource_changes": [` + strings.Join([]string{
		change("a", "by_hostname", "rhostname001"),
		change("a", "by_part_of_a_name", "pve-rorgname0001-x"),
		change("a", "by_address", "198.18.4.5"),
		change("a", "by_token", "rtoken000001"),
		`{"address": "module.site[\"rorgname0001\"].a.in_a_keyed_module", "type": "a", "name": "in_a_keyed_module"}`,
		change("a", "by_node_key", "node0"),
		change("a", "by_public_literal", "public"),
		change("a", "by_attestation", "cloudflare"),
		change("a", "by_short_value", "ab"),
		`{"address": "a.by_number[100]", "type": "a", "name": "by_number", "index": 100}`,
		change("a", "unkeyed", ""),
	}, ",") + `]}`
	got, err := KeyedByAVaultValue([]byte(plan), tpl, config)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"a.by_address is keyed by sites.site0.nodes[0].ip",
		"a.by_hostname is keyed by sites.site0.nodes[0].hostname",
		"a.by_part_of_a_name is keyed by organization.name",
		"a.by_token is keyed by tunnel.token",
		"a.in_a_keyed_module is keyed by organization.name",
	}
	if !slices.Equal(got, want) {
		t.Errorf("got:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
	if _, err := KeyedByAVaultValue([]byte("not json"), tpl, config); err == nil {
		t.Error("a plan that is not JSON was called clean")
	}
}

// A root nothing has been built from has a record like any other: it reads
// back, holds no resources, and a change planned against it gets a stand-in
// for every vault value - with an attestation taking the provider written
// beside it, since nothing else would pass the check it is compared in.
func TestAnUnbuiltRootHasARecordToPlanAgainst(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "unbuilt")
	if err := WriteUnbuilt(dir, "site7"); err != nil {
		t.Fatal(err)
	}
	state, config, meta, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Site != "site7" || len(config) != 0 || len(state["resources"].([]any)) != 0 || state["lineage"] == "" {
		t.Errorf("an unbuilt record read back as state %v, config %v, meta %+v", state, config, meta)
	}

	tpl := mustDecode(t, `{"storage": {"provider": "acme", "vault_provider": "{{ op://estate/storage/provider }}",
	  "token": "{{ op://estate/storage/token }}"},
	  "unattested": {"vault_provider": "{{ op://estate/other/provider }}"}}`)
	got := Reconcile(mustFingerprinter(t, 1), "", tpl, config).(map[string]any)
	storage := got["storage"].(map[string]any)
	if storage["vault_provider"] != "acme" {
		t.Errorf("an attestation with no record took %v, not the provider beside it", storage["vault_provider"])
	}
	if token, _ := storage["token"].(string); token == "" || strings.Contains(token, "op://") {
		t.Errorf("a vault value with no record was not given a stand-in: %q", token)
	}
	// With no provider beside it there is nothing to take, and it is a
	// stand-in like any other vault value.
	if v, _ := got["unattested"].(map[string]any)["vault_provider"].(string); v == "" || strings.Contains(v, "op://") {
		t.Errorf("got %q", v)
	}
	// A recorded attestation is kept: the record is what the estate holds.
	recorded := mustDecode(t, `{"storage": {"vault_provider": "recorded"}}`)
	if got := Reconcile(mustFingerprinter(t, 1), "", tpl, recorded).(map[string]any)["storage"].(map[string]any)["vault_provider"]; got != "recorded" {
		t.Errorf("a recorded attestation was replaced by %v", got)
	}
}

// The stand-in for a cluster's access has the shape the real one has: a
// certificate and a key that parse and match, so a provider configures.
func TestUnbuiltAccessIsShapedLikeAClustersAccess(t *testing.T) {
	vars := UnbuiltAccess("structured", "plain")
	var access map[string]string
	if err := json.Unmarshal([]byte(vars["structured"]), &access); err != nil {
		t.Fatal(err)
	}
	cert, errC := base64.StdEncoding.DecodeString(access["client_certificate"])
	key, errK := base64.StdEncoding.DecodeString(access["client_key"])
	if errC != nil || errK != nil {
		t.Fatalf("the certificate or the key is not base64: %v, %v", errC, errK)
	}
	if _, err := tls.X509KeyPair(cert, key); err != nil {
		t.Errorf("the stand-in certificate and key do not make a pair a provider would accept: %v", err)
	}
	if !strings.HasPrefix(access["host"], "https://") || access["ca_certificate"] == "" || vars["plain"] == "" {
		t.Errorf("the access is %v, and the plain form %q", access, vars["plain"])
	}
}
