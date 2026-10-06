package holds

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func asset(lifetime, livesOn string) Asset {
	return Asset{What: "the thing", Lifetime: lifetime, LivesOn: livesOn, HeldBy: "PersistentVolumeClaim/thing", Owner: "app", Path: "app/declaration"}
}

// The one question, asked of every pairing that matters: destroying
// something endangers an asset only when the asset should outlive it and
// lives inside it.
func TestDestroyingSomethingEndangersOnlyWhatShouldOutliveItAndLivesInIt(t *testing.T) {
	for _, c := range []struct {
		lifetime, livesOn string
		destroying        Scope
		want              bool
		why               string
	}{
		{"client", "machine", Site, true, "a client's data on a machine goes with the site the machine is in"},
		{"client", "machine", Machine, true, "and with the machine"},
		{"site", "machine", Site, false, "a site's own history is worth nothing once the site is gone"},
		{"site", "machine", Machine, true, "but must not die with one machine"},
		{"site", "node", Machine, false, "what lives on the node is not inside a machine"},
		{"estate", "site", Site, true, "the estate's data kept in one site goes with it"},
		{"machine", "machine", Machine, false, "a machine's readings die with the machine, as they should"},
	} {
		if got := asset(c.lifetime, c.livesOn).EndangeredBy(c.destroying); got != c.want {
			t.Errorf("worth keeping for a %s's life, living on a %s, destroying a %s: endangered = %v, and %s", c.lifetime, c.livesOn, c.destroying, got, c.why)
		}
	}
}

func TestADeclarationThatCannotAnswerTheQuestionIsRefused(t *testing.T) {
	copyOf := func(a Asset, storage, under, mayLose string) Asset {
		a.Copy, a.MayLose = &Copy{Storage: storage, Under: under}, mayLose
		return a
	}
	core := asset("site", "machine")
	core.Owner = Core
	for name, c := range map[string]struct {
		a    Asset
		want string
	}{
		"no name":                         {Asset{Lifetime: "site", LivesOn: "machine", HeldBy: "A/b", Path: "p"}, "what"},
		"held by nothing":                 {Asset{What: "x", Lifetime: "site", LivesOn: "machine", Path: "p"}, "held_by"},
		"held by something not an object": {func() Asset { a := asset("site", "machine"); a.HeldBy = "world"; return a }(), "Kind/name"},
		"a lifetime that is not a scope":  {asset("forever", "machine"), "not a scope"},
		"living on a client":              {asset("client", "client"), "can be destroyed"},
		"outlasted by where it lives":     {asset("machine", "site"), "outlasts it"},
		"a tolerance and no copy":         {func() Asset { a := asset("site", "machine"); a.MayLose = "1h"; return a }(), "names no copy"},
		"a copy under nothing":            {copyOf(asset("client", "machine"), "", "/", "1h"), "which folder"},
		"a copy and no tolerance":         {copyOf(asset("client", "machine"), "", "worlds", ""), "gives no age"},
		"a tolerance of nothing":          {copyOf(asset("client", "machine"), "", "worlds", "0s"), "gives no age"},
		"an application naming a bucket":  {copyOf(asset("client", "machine"), "production", "worlds", "1h"), "environment a site runs it in"},
		"the core naming no bucket":       {copyOf(core, "", "postgres", "1h"), "which bucket"},
	} {
		err := c.a.Check()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: wanted a refusal saying %q, got %v", name, c.want, err)
		}
	}
	for name, a := range map[string]Asset{
		"with no copy":          asset("site", "machine"),
		"an application's copy": copyOf(asset("client", "machine"), "", "worlds", "2h"),
		"the core's copy":       copyOf(core, "database", "postgres", "1h"),
	} {
		if err := a.Check(); err != nil {
			t.Errorf("a sound declaration %s was refused: %v", name, err)
		}
	}
}

func TestTheCoresDeclarationIsReadWholeOrNotAtAll(t *testing.T) {
	got, err := ParseCore([]byte(`{"_comment": ["x"], "holds": [{"what": "history", "lifetime": "site", "lives_on": "machine", "held_by": "HelmRelease/metrics"}]}`))
	if err != nil || len(got) != 1 || got[0].Owner != Core || got[0].Path != CoreFile {
		t.Fatalf("the core's declaration was read as %+v, %v", got, err)
	}
	for name, body := range map[string]string{
		"a field nothing reads":       `{"holds": [], "keeps": []}`,
		"an asset that is not sound":  `{"holds": [{"what": "history", "lifetime": "site", "lives_on": "machine"}]}`,
		"a list where a file belongs": `[]`,
	} {
		if _, err := ParseCore([]byte(body)); err == nil {
			t.Errorf("%s was read as a declaration", name)
		}
	}
	if got, err := ReadCore(t.TempDir()); err != nil || got != nil {
		t.Errorf("a repository with no such file was read as %+v, %v", got, err)
	}

	root := t.TempDir()
	path := filepath.Join(root, filepath.FromSlash(CoreFile))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"holds": [{"what": "history", "lifetime": "site", "lives_on": "machine", "held_by": "HelmRelease/metrics"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadCore(root); err != nil || len(got) != 1 || got[0].What != "history" {
		t.Errorf("the core's file was read as %+v, %v", got, err)
	}
	if err := os.WriteFile(path, []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCore(root); err == nil {
		t.Error("a core file that does not parse was read as holding nothing")
	}
}
