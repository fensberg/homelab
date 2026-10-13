package phases

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"homelab/contractor/config"
	"homelab/contractor/internal/run"
	"homelab/contractor/steps"
)

// A converge with a template to build is refused where the hypervisor's SSH
// does not answer, says which template and where to run it, and is never
// asked the question when every template is already there.
func TestAConvergeWithATemplateToBuildIsForTheWorkstation(t *testing.T) {
	cfg := &config.Config{Sites: map[string]config.Site{"site0": {
		Hypervisor: config.Hypervisor{Nodes: map[string]config.Node{"node0": {IP: "192.0.2.10"}}},
	}}}
	for name, c := range map[string]struct {
		held    []string // the templates the state holds
		zones   bool
		answers bool
		asked   bool
		refused string
	}{
		"every template is there":                    {[]string{steps.Template, steps.ZoneTemplate}, true, false, false, ""},
		"the site's is there and it has no zone":     {[]string{steps.Template}, false, false, false, ""},
		"a zone's to build, from the workstation":    {[]string{steps.Template}, true, true, true, ""},
		"a zone's to build, from inside the cluster": {[]string{steps.Template}, true, false, true, "an untrusted zone's"},
		"the site's own to build, from inside":       {nil, false, false, true, "the site's"},
		"both to build, from inside":                 {nil, true, false, true, "an untrusted zone's, the site's"},
	} {
		bin := t.TempDir()
		held := filepath.Join(bin, "held")
		if err := os.WriteFile(held, []byte(strings.Join(c.held, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		// A tofu that lists an address only when the state holds it.
		script := "#!/bin/sh\n[ \"$1 $2\" = 'state list' ] && grep -Fx \"$3\" " + held + " | sed 's/$/[\"node0\"]/'\nexit 0\n"
		if err := os.WriteFile(filepath.Join(bin, "tofu"), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

		asked := false
		before := awaitTCP
		awaitTCP = func(addr string, _, _ time.Duration) bool {
			asked = true
			if addr != "192.0.2.10:22" {
				t.Errorf("%s: asked whether %s answers", name, addr)
			}
			return c.answers
		}
		ctx := run.NewContext(t.TempDir(), "site0")
		if err := os.MkdirAll(ctx.Dir, 0o700); err != nil {
			t.Fatal(err)
		}
		err := templatesCanBeBuiltFromHere(ctx, cfg, c.zones)
		awaitTCP = before

		if asked != c.asked {
			t.Errorf("%s: the hypervisor's SSH was asked after = %v", name, asked)
		}
		var elsewhere *NotFromHereError
		switch {
		case c.refused == "" && err != nil:
			t.Errorf("%s: refused: %v", name, err)
		case c.refused != "" && !errors.As(err, &elsewhere):
			t.Errorf("%s: was not refused as a converge for the workstation: %v", name, err)
		case c.refused != "":
			said := err.Error()
			for _, want := range []string{c.refused, "task converge SITE=site0", "Nothing has been changed"} {
				if !strings.Contains(said, want) {
					t.Errorf("%s: the refusal does not say %q:\n%s", name, want, said)
				}
			}
		}
	}
}
