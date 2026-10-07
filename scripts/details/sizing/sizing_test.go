package sizing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A size is believed only for what its source can bear. A publisher's figure
// and an estimate need saying what they are; a measurement needs the window
// it was taken over and what the thing was doing, because a reading from a
// thing that sat idle is the mistake this exists to stop.
func TestASourceSaysEnoughToBeBelievedOrIsRefused(t *testing.T) {
	for name, c := range map[string]struct {
		s    Source
		want string
	}{
		"a publisher's figure":               {Source{Kind: Publisher, Detail: "the vendor's guide"}, ""},
		"an estimate":                        {Source{Kind: Estimate, Detail: "twice the chart's default"}, ""},
		"a measurement, with its window":     {Source{Kind: Measured, Detail: "peak plus a fifth", Over: "720h", Load: "ten players, a built-up world"}, ""},
		"no detail":                          {Source{Kind: Estimate}, "gives no detail"},
		"a kind that is not one":             {Source{Kind: "guess", Detail: "x"}, "is not one of"},
		"a measurement with no window":       {Source{Kind: Measured, Detail: "x", Load: "players"}, "not over how long"},
		"a measurement of an afternoon":      {Source{Kind: Measured, Detail: "x", Over: "3h", Load: "players"}, "a day or more"},
		"a measurement of a thing left idle": {Source{Kind: Measured, Detail: "x", Over: "720h"}, "sat idle"},
		"an estimate that claims a window":   {Source{Kind: Estimate, Detail: "x", Over: "720h"}, "only a measured size was watched"},
	} {
		err := c.s.Check("the thing")
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s was refused: %v", name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: wanted a refusal saying %q, got %v", name, c.want, err)
		}
	}
}

func TestTheCoresDeclarationIsReadWholeOrNotAtAll(t *testing.T) {
	good := `{"_comment": ["x"], "sized_from": {"HelmRelease/a": {"*": {"kind": "estimate", "basis": "chosen"}}}}`
	got, err := ParseCore([]byte(good))
	if err != nil || got["HelmRelease/a"][EveryPod].Kind != Estimate {
		t.Fatalf("read as %+v, %v", got, err)
	}
	for name, body := range map[string]string{
		"a field nothing reads":         `{"sized_from": {}, "sizes": {}}`,
		"an object that is not one":     `{"sized_from": {"a": {"*": {"kind": "estimate", "basis": "x"}}}}`,
		"an object with nothing said":   `{"sized_from": {"HelmRelease/a": {}}}`,
		"a source that is not believed": `{"sized_from": {"HelmRelease/a": {"*": {"kind": "measured", "basis": "x"}}}}`,
		"a list where a file belongs":   `[]`,
	} {
		if _, err := ParseCore([]byte(body)); err == nil {
			t.Errorf("%s was read as a declaration", name)
		}
	}

	root := t.TempDir()
	if got, err := ReadCore(root); err != nil || got != nil {
		t.Errorf("a repository with no such file was read as %+v, %v", got, err)
	}
	path := filepath.Join(root, filepath.FromSlash(CoreFile))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadCore(root); err != nil || len(got) != 1 {
		t.Errorf("the core's file was read as %+v, %v", got, err)
	}
	if err := os.WriteFile(path, []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCore(root); err == nil {
		t.Error("a core file that does not parse was read as saying nothing")
	}
}
