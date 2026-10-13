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
		"nobody to bill":                {func(c *config) { c.SourceControl.BillingEmail = "" }, "source-control/billing_email"},
		"no App that delivers":          {func(c *config) { c.SourceControl.ProcurementAppID = "" }, "source-control/procurement_app_id"},
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

// whatStands answers for the source control from what a test gives it, and
// counts how often it was asked.
type whatStands struct {
	held    map[string]int64
	org     int64
	err     error
	lookups int
}

func (w *whatStands) rulesets(string) (map[string]int64, error) {
	w.lookups++
	return w.held, w.err
}

func (w *whatStands) organisation() (int64, error) {
	w.lookups++
	return w.org, w.err
}

const aPlanThatMakesRulesets = `{"format_version":"1.2","resource_changes":[
  {"address":"module.repository[\"homelab\"].github_repository_ruleset.stands","mode":"managed","type":"github_repository_ruleset",
   "change":{"actions":["create"],"after":{"name":"one","repository":"homelab"}}},
  {"address":"module.repository[\"homelab\"].github_repository_ruleset.new","mode":"managed","type":"github_repository_ruleset",
   "change":{"actions":["create"],"after":{"name":"not there yet","repository":"homelab"}}},
  {"address":"module.repository[\"homelab\"].github_repository_ruleset.kept","mode":"managed","type":"github_repository_ruleset",
   "change":{"actions":["update"],"after":{"name":"two","repository":"homelab"}}},
  {"address":"github_organization_settings.estate","mode":"managed","type":"github_organization_settings","change":{"actions":["create"],"after":{}}},
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
	there := &whatStands{held: map[string]int64{"one": 11, "two": 22}, org: 9001}

	if err := e.adoptSourceControl(there); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	want := "import {\n  to = module.repository[\"homelab\"].github_repository_ruleset.stands\n  id = \"homelab:11\"\n}\n" +
		"import {\n  to = github_organization_settings.estate\n  id = \"9001\"\n}\n"
	if string(written) != want {
		t.Errorf("the adoptions were written as\n%s\nand are\n%s", written, want)
	}
	if there.lookups != 2 {
		t.Errorf("the source control was asked %d times for one repository and one organisation", there.lookups)
	}
	if len(*calls) != 0 || strings.Contains(asked(), "import") {
		t.Errorf("something was imported by command: %v\n%s", *calls, asked())
	}
	if left, _ := filepath.Glob(filepath.Join(e.dir, "*.tfplan")); len(left) != 0 {
		t.Errorf("a saved plan was left behind: %v", left)
	}

	// A source control that cannot be asked stops the run: the apply would
	// otherwise make a second of every rule, and write the organisation's
	// settings over. And what an earlier run wrote does not outlive it.
	if err := e.adoptSourceControl(&whatStands{err: errors.New("refused")}); err == nil {
		t.Error("a source control that could not be asked was read as holding nothing")
	}
	if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) {
		t.Error("adoptions an earlier run wrote were left for this one to apply")
	}
}

// A plan that makes nothing asks the source control nothing, and a ruleset
// planned without a name cannot be matched and is refused.
func TestOnlyAPlanThatMakesRulesetsAsksTheSourceControl(t *testing.T) {
	tofuStandIn(t, aPlanThatChangesNothing)
	e, _ := fakeEstate(t)
	e.dir = t.TempDir()
	there := &whatStands{}
	if err := e.adoptSourceControl(there); err != nil || there.lookups != 0 {
		t.Fatalf("the source control was asked %d times when the plan makes nothing there: %v", there.lookups, err)
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

// The organisation is asked after by the name in the address, as the
// estate's App, and an answer without its number is not one.
func TestTheOrganisationIsKnownByThePlatformsNumberForIt(t *testing.T) {
	for name, c := range map[string]struct {
		status int
		body   string
		want   int64
		says   string
	}{
		"an organisation":          {200, `{"id":9001,"login":"example"}`, 9001, ""},
		"a refusal":                {404, `{"message":"Not Found"}`, 0, "asking after the estate's organisation"},
		"an answer with no number": {200, `{"login":"example"}`, 0, "without its number"},
	} {
		base, credential := ghapitest.Server(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/orgs/example" {
				t.Errorf("%s: asked %s", name, r.URL.Path)
			}
			w.WriteHeader(c.status)
			_, _ = w.Write([]byte(c.body))
		})
		old := sourceControlBase
		sourceControlBase = base
		cfg := validConfig()
		cfg.SourceControl.AppCredential = credential
		s, err := cfg.sourceControl()
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.organisation()
		sourceControlBase = old
		if got != c.want || (c.says == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.says)) {
			t.Errorf("%s: got %d, %v", name, got, err)
		}
	}
}
