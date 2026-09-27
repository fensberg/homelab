package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func template(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tpl.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// With one site declared, not naming it is fine; with two, a command that
// does not say which is refused, and the refusal lists them. A named site
// must exist.
func TestASiteIsNamedOrItIsTheOnlyOne(t *testing.T) {
	one := template(t, `{"sites": {"north": {}}}`)
	two := template(t, `{"sites": {"south": {}, "north": {}}}`)

	if got, err := ResolveSite("", one); err != nil || got != "north" {
		t.Errorf("the only site: got %q, %v", got, err)
	}
	if _, err := ResolveSite("", two); err == nil || !strings.Contains(err.Error(), "north, south") {
		t.Errorf("an unnamed site among two: %v", err)
	}
	if got, err := ResolveSite("south", two); err != nil || got != "south" {
		t.Errorf("a named site: got %q, %v", got, err)
	}
	if _, err := ResolveSite("west", two); err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Errorf("an undeclared site: %v", err)
	}
}

func TestATemplateWithoutSitesIsAnError(t *testing.T) {
	for name, path := range map[string]string{
		"no sites":    template(t, `{"sites": {}}`),
		"not JSON":    template(t, `not json`),
		"not present": filepath.Join(t.TempDir(), "missing.json"),
	} {
		if _, err := DeclaredSites(path); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
