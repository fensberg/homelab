package asbuilt

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// wholeTofu stands in for tofu: it records what it is asked, makes the
// provider cache an init would, and answers show with an empty plan.
type wholeTofu struct {
	asked [][]string
	env   []string
}

func (f *wholeTofu) run(dir string, env []string, args ...string) ([]byte, []byte, error) {
	f.asked = append(f.asked, args)
	f.env = env
	switch args[0] {
	case "init":
		if err := os.MkdirAll(filepath.Join(dir, ".terraform", "providers", "registry.example"), 0o700); err != nil {
			return nil, nil, err
		}
	case "show":
		return []byte(`{"format_version":"1.2","resource_changes":[],"planned_values":{"root_module":{}}}`), nil, nil
	}
	return nil, nil, nil
}

// wholeRoot is a root two levels below a made-up repository, with a module
// beside it, and a workspace at the same depth.
func wholeRoot(t *testing.T) (in WholeInputs) {
	t.Helper()
	top := t.TempDir()
	root := filepath.Join(top, "roots", "estate")
	for rel, body := range map[string]string{
		"main.tf":              "module \"m\" {\n  source = \"./m\"\n}\n",
		"m/main.tf":            "resource \"x\" \"y\" {}\n",
		ProviderLock:           "# lock\n",
		"tests/fixture.tftest": "# not read\n",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	work := filepath.Join(top, ".work", "estate")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	return WholeInputs{
		Root: root, Work: work,
		Files:     []string{"main.tf", "m/main.tf", ProviderLock},
		Template:  []byte(`{"access": {"api_token": "{{ op://estate/access/api_token }}", "provider": "cloudflare"}}`),
		Providers: filepath.Join(top, "providers"),
	}
}

// A root is planned whole: the files it is given, the module beside it
// among them, a local backend over its own, and a config with a stand-in
// for every vault value and the template's literals kept.
func TestARootIsPlannedWholeWithAStandInForEveryVaultValue(t *testing.T) {
	in := wholeRoot(t)
	f := &wholeTofu{}
	if _, err := PlanWhole(in, f.run); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"main.tf", "m/main.tf", wholeBackend, wholeConfig} {
		if _, err := os.Stat(filepath.Join(in.Work, rel)); err != nil {
			t.Errorf("the copy has no %s: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(in.Work, "tests", "fixture.tftest")); err == nil {
		t.Error("a file the root was not given was copied")
	}
	raw, err := os.ReadFile(filepath.Join(in.Work, wholeConfig))
	if err != nil {
		t.Fatal(err)
	}
	var config struct{ Access map[string]string }
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "{{") || len(config.Access["api_token"]) != 40 || config.Access["provider"] != "cloudflare" {
		t.Errorf("the config is %s", raw)
	}
	if !slices.Contains(f.env, "TF_VAR_offline=true") || !slices.Contains(f.env, "TF_VAR_config_path="+filepath.Join(in.Work, wholeConfig)) {
		t.Errorf("the plan is not made offline from the stand-in config: %v", f.env)
	}
	var verbs []string
	for _, a := range f.asked {
		verbs = append(verbs, a[0])
	}
	if !slices.Equal(verbs, []string{"init", "plan", "show"}) {
		t.Errorf("tofu was asked %v", verbs)
	}
}

// The providers one plan fetched are kept by the lock that chose them, and
// the next plan of a root with that lock is made from them.
func TestARootsProvidersAreKeptForItsNextPlan(t *testing.T) {
	in := wholeRoot(t)
	first := &wholeTofu{}
	if _, err := PlanWhole(in, first.run); err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(first.asked[0], func(a string) bool { return strings.HasPrefix(a, "-plugin-dir=") }) {
		t.Error("the first plan was told of providers nothing had kept")
	}
	again := wholeRoot(t)
	again.Providers = in.Providers
	again.Root, again.Work = in.Root, filepath.Join(filepath.Dir(in.Work), "again")
	if err := os.MkdirAll(again.Work, 0o700); err != nil {
		t.Fatal(err)
	}
	second := &wholeTofu{}
	if _, err := PlanWhole(again, second.run); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(second.asked[0], func(a string) bool { return strings.HasPrefix(a, "-plugin-dir="+in.Providers) }) {
		t.Errorf("the second plan was not made from the kept providers: %v", second.asked[0])
	}
}

// A copy at another depth than the root's is refused before anything is
// written: its references up the tree would lead elsewhere.
func TestARootCopiedToAnotherDepthIsRefused(t *testing.T) {
	in := wholeRoot(t)
	in.Work = filepath.Join(in.Work, "deeper")
	if _, err := PlanWhole(in, (&wholeTofu{}).run); err == nil || !strings.Contains(err.Error(), "at its depth") {
		t.Errorf("got %v", err)
	}
}
