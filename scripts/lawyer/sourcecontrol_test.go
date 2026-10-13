package main

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homelab/details/ghapi/ghapitest"
)

// aSourceControl is a config whose App holds a real key, and a local server
// standing in for the source control that answers the sign-in and then
// whatever is given for the list of rulesets.
func aSourceControl(t *testing.T, status int, rulesets string) config {
	t.Helper()
	base, credential := ghapitest.Server(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/example/homelab/rulesets" {
			t.Errorf("asked %s", r.URL.Path)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(rulesets))
	})
	old := sourceControlBase
	sourceControlBase = base
	t.Cleanup(func() { sourceControlBase = old })

	c := validConfig()
	c.SourceControl.AppCredential = credential
	return c
}

// The organisation is read out of the address every site reconciles from,
// and an address or a key of the wrong shape is refused by its vault path.
func TestTheSourceControlIsReadOutOfTheAddressAndRefusedWhenMisshapen(t *testing.T) {
	c := validConfig()
	c.SourceControl.URL = "https://example.test/example/homelab.git"
	if s, err := c.sourceControl(); err != nil || s.owner != "example" || s.app.ID != "42" || s.app.InstallationID != "77" {
		t.Fatalf("got %+v, %v", s, err)
	}
	for name, c := range map[string]struct {
		mutate func(*config)
		says   string
	}{
		"an address with no repository": {func(c *config) { c.SourceControl.URL = "https://example.test/example" }, "the address of the estate's repository"},
		"a key that was pasted as PEM":  {func(c *config) { c.SourceControl.PrivateKey = "-----BEGIN-----" }, "source-control/private_key"},
		"no App":                        {func(c *config) { c.SourceControl.AppID = "" }, "source-control/app_id"},
		"no installation":               {func(c *config) { c.SourceControl.InstallationID = " " }, "source-control/installation_id"},
	} {
		cfg := validConfig()
		c.mutate(&cfg)
		if err := cfg.validate(); err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: want an error naming %q, got %v", name, c.says, err)
		}
	}
}

// The rulesets a repository holds are read by name, as the estate's App; a
// refusal is an error and never "none", and two of one name are refused.
func TestRulesetsAreReadByNameAndARefusalIsNeverNone(t *testing.T) {
	s, err := aSourceControl(t, 200, `[{"id":11,"name":"one"},{"id":22,"name":"two"}]`).sourceControl()
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.rulesets("homelab"); err != nil || len(got) != 2 || got["two"] != 22 {
		t.Errorf("got %v, %v", got, err)
	}

	s, _ = aSourceControl(t, 403, `{"message":"Resource not accessible by integration"}`).sourceControl()
	if got, err := s.rulesets("homelab"); err == nil || got != nil || !strings.Contains(err.Error(), "may lack the permission") {
		t.Errorf("a refusal was read as %v, %v", got, err)
	}

	s, _ = aSourceControl(t, 200, `[{"id":11,"name":"one"},{"id":22,"name":"one"}]`).sourceControl()
	if _, err := s.rulesets("homelab"); err == nil || !strings.Contains(err.Error(), "two rulesets named") {
		t.Errorf("two rulesets of one name were read as one: %v", err)
	}

	s.app.Key = []byte("not a key")
	if _, err := s.rulesets("homelab"); err == nil || !strings.Contains(err.Error(), "signing in as the estate's App") {
		t.Errorf("a sign-in that cannot be made: %v", err)
	}
}

const aPlanThatMakesRulesets = `{"format_version":"1.2","resource_changes":[
  {"address":"module.repository[\"homelab\"].github_repository_ruleset.stands","mode":"managed","type":"github_repository_ruleset",
   "change":{"actions":["create"],"after":{"name":"one","repository":"homelab"}}},
  {"address":"module.repository[\"homelab\"].github_repository_ruleset.new","mode":"managed","type":"github_repository_ruleset",
   "change":{"actions":["create"],"after":{"name":"not there yet","repository":"homelab"}}},
  {"address":"module.repository[\"homelab\"].github_repository_ruleset.kept","mode":"managed","type":"github_repository_ruleset",
   "change":{"actions":["update"],"after":{"name":"two","repository":"homelab"}}},
  {"address":"thing.new","mode":"managed","type":"thing","change":{"actions":["create"],"after":{"name":"one","repository":"homelab"}}}
]}`

// A ruleset the estate declares and the repository already holds is adopted
// by an import written for this run; one the repository lacks is left for
// the apply to make; one already the estate's is left alone; and nothing is
// imported by command, which would change the estate's record.
func TestAStandingRulesetIsAdoptedByAnImportWrittenForThisRun(t *testing.T) {
	asked := tofuStandIn(t, aPlanThatMakesRulesets)
	e, calls := fakeEstate(t)
	e.dir = t.TempDir()
	file := filepath.Join(e.dir, adoptionsFile)
	lookups := 0
	listed := func(repository string) (map[string]int64, error) {
		lookups++
		if repository != "homelab" {
			t.Errorf("asked about %s", repository)
		}
		return map[string]int64{"one": 11, "two": 22}, nil
	}

	if err := e.adoptRulesets(listed); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	want := "import {\n  to = module.repository[\"homelab\"].github_repository_ruleset.stands\n  id = \"homelab:11\"\n}\n"
	if string(written) != want {
		t.Errorf("the adoptions were written as\n%s\nand are\n%s", written, want)
	}
	if lookups != 1 {
		t.Errorf("the repository was asked %d times for one plan", lookups)
	}
	if len(*calls) != 0 || strings.Contains(asked(), "import") {
		t.Errorf("something was imported by command: %v\n%s", *calls, asked())
	}
	if left, _ := filepath.Glob(filepath.Join(e.dir, "*.tfplan")); len(left) != 0 {
		t.Errorf("a saved plan was left behind: %v", left)
	}

	// A repository that holds none of them: nothing to adopt, and what an
	// earlier run wrote does not outlive it.
	if err := e.adoptRulesets(func(string) (map[string]int64, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) {
		t.Error("adoptions an earlier run wrote were left for this one to apply")
	}

	// A repository that cannot be asked stops the run: the apply would
	// otherwise make a second of everything.
	if err := e.adoptRulesets(func(string) (map[string]int64, error) { return nil, errors.New("refused") }); err == nil {
		t.Error("a repository that could not be asked was read as holding nothing")
	}
}

// A plan that makes nothing asks the source control nothing, and a ruleset
// planned without a name cannot be matched and is refused.
func TestOnlyAPlanThatMakesRulesetsAsksTheSourceControl(t *testing.T) {
	tofuStandIn(t, aPlanThatChangesNothing)
	e, _ := fakeEstate(t)
	e.dir = t.TempDir()
	if err := e.adoptRulesets(func(string) (map[string]int64, error) {
		t.Error("the source control was asked when the plan makes no ruleset")
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := plannedRulesets([]byte(`{"format_version":"1.2","resource_changes":[
	  {"address":"github_repository_ruleset.x","mode":"managed","type":"github_repository_ruleset","change":{"actions":["create"],"after":{"repository":"homelab"}}}]}`)); err == nil {
		t.Error("a ruleset planned without a name was not refused")
	}
	if _, err := plannedRulesets([]byte("not a plan")); err == nil {
		t.Error("something that is not a plan was read as one that makes nothing")
	}
}

// A demolish releases every bucket and everything set at the source
// control, however deep in a module it is declared, and nothing else.
func TestADemolishReleasesWhatOutlivesTheEstateAndNothingElse(t *testing.T) {
	got := outlivingTheEstate([]string{
		`module.site["site0"].cloudflare_r2_bucket.production`,
		`module.repository["homelab"].github_repository_ruleset.releases`,
		`module.repository["homelab"].github_actions_repository_permissions.this`,
		`github_organization_settings.estate`,
		`module.site["site0"].cloudflare_account_token.production`,
		`cloudflare_zero_trust_access_application.enrollment`,
		`data.github_organization.estate`,
	})
	want := []string{
		`module.site["site0"].cloudflare_r2_bucket.production`,
		`module.repository["homelab"].github_repository_ruleset.releases`,
		`module.repository["homelab"].github_actions_repository_permissions.this`,
		`github_organization_settings.estate`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("released\n%s\nand should release\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
