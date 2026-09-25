package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The lawyer holds the estate's credentials and only those. A token reaching a
// second vault can read a site's credentials, so it is refused rather than
// used with care.
func TestTheLawyerRefusesATokenThatReachesMoreThanTheEstate(t *testing.T) {
	for _, ok := range [][]string{
		{"estate"},
		{"estate", "estate-shared", "site0-shared"},
	} {
		if err := checkVaults(ok); err != nil {
			t.Errorf("%v was refused: %v", ok, err)
		}
	}
	if err := checkVaults([]string{"estate-shared", "site0-shared"}); err == nil {
		t.Error("a token that cannot see the estate vault was accepted")
	}
	err := checkVaults([]string{"estate", "estate-shared", "site0"})
	if err == nil {
		t.Fatal("a token reaching a site's own vault was accepted, so the lawyer could read what a site generates for itself")
	}
	if strings.Contains(err.Error(), "site0") {
		t.Errorf("the refusal names another vault, and it reaches a public log: %v", err)
	}
}

// Build establishes and converge maintains, and each refuses the other's case.
func TestEachVerbRefusesTheEstateItWasNotMeantFor(t *testing.T) {
	standing := []string{enrollmentAddress}
	for _, c := range []struct {
		verb      string
		resources []string
		refuse    string
	}{
		{"build-estate", nil, ""},
		{"build-estate", standing, "Use converge-estate"},
		{"converge-estate", standing, ""},
		{"converge-estate", nil, "Use build-estate"},
		{"demolish-estate", standing, ""},
		{"demolish-estate", nil, "Use build-estate"},
	} {
		err := admit(c.verb, c.resources)
		switch {
		case c.refuse == "" && err != nil:
			t.Errorf("%s over %d resource(s) was refused: %v", c.verb, len(c.resources), err)
		case c.refuse != "" && (err == nil || !strings.Contains(err.Error(), c.refuse)):
			t.Errorf("%s over %d resource(s): want a refusal saying %q, got %v", c.verb, len(c.resources), c.refuse, err)
		}
	}
}

func validConfig() config {
	var c config
	c.Access.Provider = "cloudflare"
	c.Access.VaultProvider = "cloudflare"
	c.Access.AccountID = "fixture-account"
	c.Access.APIToken = "fixture-token"
	c.Access.Members = "someone@example.com"
	c.State.Bucket = "example-estate"
	c.State.AccessKeyID = "fixture-key"
	c.State.SecretAccessKey = "fixture-secret"
	return c
}

// An empty field is refused at Render, by its vault path, rather than as a
// 401 or an empty policy several steps later. Values never reach the message.
func TestTheRenderedEstateConfigIsRefusedWhenAFieldIsMissing(t *testing.T) {
	if err := validConfig().validate(); err != nil {
		t.Fatalf("a complete config was refused: %v", err)
	}
	for _, c := range []struct {
		name   string
		mutate func(*config)
		want   string
	}{
		{"nobody may enroll", func(c *config) { c.Access.Members = " " }, "access/members"},
		{"no bucket", func(c *config) { c.State.Bucket = "" }, "state/bucket"},
		{"no token", func(c *config) { c.Access.APIToken = "" }, "access/api_token"},
		{"another vendor's item", func(c *config) { c.Access.VaultProvider = "aws" }, "attests a provider"},
		{"the estate declared for a vendor it does not implement", func(c *config) { c.Access.Provider = "aws" }, "implements cloudflare"},
	} {
		cfg := validConfig()
		c.mutate(&cfg)
		err := cfg.validate()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want an error naming %q, got %v", c.name, c.want, err)
		}
	}
}

func serve(t *testing.T, path, body string, status int) cloudflare {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Errorf("the request did not carry the estate token")
		}
		if r.URL.Path != "/accounts/fixture-account"+path {
			t.Errorf("asked for %s, want %s", r.URL.Path, path)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	old := apiBase
	apiBase = srv.URL
	t.Cleanup(func() { apiBase = old })
	return cloudflare{account: "fixture-account", token: "fixture-token"}
}

// The enrollment application is found by its type, so a renamed one is still
// adopted and a self-hosted application is never mistaken for it.
func TestTheEnrollmentApplicationIsFoundByItsType(t *testing.T) {
	api := serve(t, "/access/apps", `{"success":true,"result":[
		{"id":"app-1","type":"self_hosted"},{"id":"app-2","type":"warp"}]}`, 200)
	if id, err := api.enrollmentApp(); err != nil || id != "app-2" {
		t.Fatalf("got %q, %v; want app-2", id, err)
	}

	api = serve(t, "/access/apps", `{"success":true,"result":[{"id":"app-1","type":"self_hosted"}]}`, 200)
	if id, err := api.enrollmentApp(); err != nil || id != "" {
		t.Fatalf("an account with no enrollment application: got %q, %v", id, err)
	}

	api = serve(t, "/access/apps", `{"success":true,"result":[{"id":"a","type":"warp"},{"id":"b","type":"warp"}]}`, 200)
	if _, err := api.enrollmentApp(); err == nil {
		t.Fatal("two enrollment applications were accepted, and one would be adopted at random")
	}
}

// Every tunnel in the account is a site's, so any live one refuses a demolish.
func TestLiveTunnelsCountsTheSitesStandingOnTheEstate(t *testing.T) {
	api := serve(t, "/cfd_tunnel", `{"success":true,"result":[{"id":"t1"},{"id":"t2"}]}`, 200)
	if n, err := api.liveTunnels(); err != nil || n != 2 {
		t.Fatalf("got %d, %v; want 2", n, err)
	}
}

// A refusal from Cloudflare is an error naming its reason, never an empty
// answer. An empty tunnel list read from a 403 would let a demolish through
// while a site still stands.
func TestARefusalFromCloudflareIsAnErrorNotAnEmptyAnswer(t *testing.T) {
	api := serve(t, "/cfd_tunnel", `{"success":false,"errors":[{"code":9109,"message":"Unauthorized to access requested resource"}],"result":null}`, 403)
	n, err := api.liveTunnels()
	if err == nil {
		t.Fatalf("a 403 was read as %d live tunnels", n)
	}
	if !strings.Contains(err.Error(), "Unauthorized") || !strings.Contains(err.Error(), "permission") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}

func fakeEstate(t *testing.T) (*estate, *[][]string) {
	t.Helper()
	var calls [][]string
	e := &estate{cfg: validConfig()}
	e.run = func(args ...string) error {
		calls = append(calls, args)
		return nil
	}
	return e, &calls
}

// The account's one enrollment application is imported when state lacks it,
// left alone when state holds it, and created by the apply when the account
// has none - which is what a demolished estate leaves.
func TestTheEnrollmentApplicationIsAdoptedOnlyWhenItExistsOutsideState(t *testing.T) {
	api := serve(t, "/access/apps", `{"success":true,"result":[{"id":"app-2","type":"warp"}]}`, 200)

	e, calls := fakeEstate(t)
	if err := e.adoptEnrollment(api, nil); err != nil {
		t.Fatal(err)
	}
	want := []string{"import", "-input=false", enrollmentAddress, "fixture-account/app-2"}
	if len(*calls) != 1 || strings.Join((*calls)[0], " ") != strings.Join(want, " ") {
		t.Fatalf("an application outside state: tofu was asked %v, want %v", *calls, want)
	}

	e, calls = fakeEstate(t)
	if err := e.adoptEnrollment(api, []string{enrollmentAddress}); err != nil || len(*calls) != 0 {
		t.Fatalf("an application already in state was imported again: %v, %v", *calls, err)
	}

	api = serve(t, "/access/apps", `{"success":true,"result":[]}`, 200)
	e, calls = fakeEstate(t)
	if err := e.adoptEnrollment(api, nil); err != nil || len(*calls) != 0 {
		t.Fatalf("an account with no application: tofu was asked %v, %v", *calls, err)
	}
}

// The bucket's credential reaches tofu through its environment alone, pointed
// at the estate account's own object storage.
func TestTheBackendIsReachedWithTheEstateBucketsOwnCredential(t *testing.T) {
	e, _ := fakeEstate(t)
	env := strings.Join(e.backendEnv(), "\n")
	for _, want := range []string{
		"AWS_ACCESS_KEY_ID=fixture-key",
		"AWS_SECRET_ACCESS_KEY=fixture-secret",
		"AWS_ENDPOINT_URL_S3=https://fixture-account.r2.cloudflarestorage.com",
	} {
		if !strings.Contains(env, want) {
			t.Errorf("tofu's environment is missing %s", want)
		}
	}
}

// Sterilize removes the rendered secrets and the backend record however the
// run ended, including when there is nothing to remove.
func TestSterilizeRemovesWhatTheRunWrote(t *testing.T) {
	dir := t.TempDir()
	e := &estate{dir: dir, rendered: filepath.Join(dir, "estate.rendered.json")}
	record := filepath.Join(dir, ".terraform", "terraform.tfstate")
	if err := os.MkdirAll(filepath.Dir(record), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{e.rendered, record} {
		if err := os.WriteFile(p, []byte("secret"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	e.sterilize()
	for _, p := range []string{e.rendered, record} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s survived sterilize", filepath.Base(p))
		}
	}
	e.sterilize() // nothing left: must not fail or warn
}

// The first build has no state at all, and that must read as an empty estate
// rather than fail; state that is present and unreadable must fail rather than
// read as empty, or build-estate would build over it.
func TestStateIsReadAsEmptyOnlyWhenThereIsNone(t *testing.T) {
	for _, none := range []string{"", "\n"} {
		got, err := stateResources([]byte(none))
		if err != nil || len(got) != 0 {
			t.Errorf("no state %q: got %v, %v", none, got, err)
		}
	}

	got, err := stateResources([]byte(`{"version":4,"serial":3,"lineage":"l","resources":[
		{"mode":"data","type":"cloudflare_accounts","name":"a"},
		{"mode":"managed","type":"cloudflare_zero_trust_access_application","name":"enrollment"},
		{"mode":"managed","type":"cloudflare_zero_trust_split_tunnel","name":"estate"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, " ") != enrollmentAddress+" cloudflare_zero_trust_split_tunnel.estate" {
		t.Errorf("got %v; want the two managed resources and no data source", got)
	}

	if got, err := stateResources([]byte(`{"version":4,"serial":1,"lineage":"l","resources":[]}`)); err != nil || len(got) != 0 {
		t.Errorf("an emptied estate: got %v, %v", got, err)
	}
	for _, bad := range []string{`not json`, `{"version":4}`} {
		if _, err := stateResources([]byte(bad)); err == nil {
			t.Errorf("unreadable state %q was taken for an empty estate", bad)
		}
	}
}
