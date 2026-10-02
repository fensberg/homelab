package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homelab/details/applications"
)

func composeFixture(t *testing.T, template string, files map[string]string) (root, tpl string) {
	t.Helper()
	root = t.TempDir()
	const committed = "config/management" + ".tpl.json"
	files[committed] = template
	for rel, body := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root, filepath.Join(root, filepath.FromSlash(committed))
}

const composeTpl = `{"sites": {"site7": {"name": "{{ op://site7-shared/identity/name }}"}, "site8": {"octet": 8}}}`

// A site's applications are added under that site and no other: each with
// the environment the site runs it in, and a wrapped reference into the
// site's own vault for every field its secrets read - the generated ones
// included, and nothing for a key that is a literal or a fact about a bucket.
func TestComposeTemplateAddsTheSitesApplications(t *testing.T) {
	root, tpl := composeFixture(t, composeTpl, map[string]string{
		applications.SiteFilePath("site7"):                       "  path: ./" + applications.Dir + "/thing/production\n",
		applications.Dir + "/thing/" + applications.Declaration:  `{"secrets": {"keys": {"a": {"vault": "user"}, "b": {"generated": "backup_key"}, "c": {"value": "x"}, "d": {"storage": "bucket"}}}}`,
		applications.Dir + "/unused/" + applications.Declaration: `{"secrets": {"keys": {"a": {"vault": "elsewhere"}}}}`,
	})
	out, err := ComposeTemplate(root, tpl, "site7")
	if err != nil {
		t.Fatal(err)
	}
	if out != filepath.Join(root, "config", ComposedTemplateFile) {
		t.Fatalf("written to %s", out)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Sites map[string]struct {
			Name         string                     `json:"name"`
			Octet        int                        `json:"octet"`
			Applications map[string]SiteApplication `json:"applications"`
		} `json:"sites"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Sites["site7"].Name != "{{ op://site7-shared/identity/name }}" || got.Sites["site8"].Octet != 8 {
		t.Errorf("what the template wrote did not survive: %s", raw)
	}
	if len(got.Sites["site8"].Applications) != 0 {
		t.Errorf("a site that was given nothing was given %v", got.Sites["site8"].Applications)
	}
	apps := got.Sites["site7"].Applications
	thing, ok := apps["thing"]
	if !ok || len(apps) != 1 {
		t.Fatalf("site7's applications are %v", apps)
	}
	if thing.Environment != "production" {
		t.Errorf("environment is %q", thing.Environment)
	}
	want := map[string]string{"user": "{{ op://site7/thing/user }}", "backup_key": "{{ op://site7/thing/backup_key }}"}
	if len(thing.Vault) != len(want) {
		t.Errorf("vault fields are %v", thing.Vault)
	}
	for field, ref := range want {
		if thing.Vault[field] != ref {
			t.Errorf("%s is %q, want %q", field, thing.Vault[field], ref)
		}
	}
	// The composed template is one the vault check and the render read.
	refs, err := VaultReferences(out)
	if err != nil || len(refs) != 3 {
		t.Errorf("the composed template's references are %v, %v", refs, err)
	}
}

// A site that was given nothing gets the committed template as it stands.
func TestComposeTemplateLeavesASiteWithNoApplicationsAlone(t *testing.T) {
	root, tpl := composeFixture(t, composeTpl, map[string]string{})
	out, err := ComposeTemplate(root, tpl, "site7")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out)
	if err != nil || string(raw) != composeTpl {
		t.Errorf("got %q, %v", raw, err)
	}
}

// What cannot be composed is refused: an application nothing declares, a site
// the template does not have, and a template that writes the section itself.
func TestComposeTemplateRefusesWhatItCannotProvideFor(t *testing.T) {
	block := "  path: ./" + applications.Dir + "/thing/production\n"
	declared := applications.Dir + "/thing/" + applications.Declaration
	for name, c := range map[string]struct {
		template string
		files    map[string]string
		want     string
	}{
		"given an application nothing declares": {composeTpl, map[string]string{applications.SiteFilePath("site7"): block}, "gives the site thing"},
		"a site the template does not have":     {`{"sites": {}}`, map[string]string{applications.SiteFilePath("site7"): block, declared: `{}`}, "declares no site"},
		"a template that names applications":    {`{"sites": {"site7": {"applications": {}}}}`, map[string]string{applications.SiteFilePath("site7"): block, declared: `{}`}, "names no application"},
		"a template that is not JSON":           {`sites:`, map[string]string{}, "not JSON"},
		"a declaration that does not read":      {composeTpl, map[string]string{applications.SiteFilePath("site7"): block, declared: `{"unread": 1}`}, "not a declaration"},
	} {
		root, tpl := composeFixture(t, c.template, c.files)
		if _, err := ComposeTemplate(root, tpl, "site7"); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
