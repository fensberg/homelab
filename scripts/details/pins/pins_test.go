package pins

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	shaA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// text is a pins file as it is written: a default, and the sites held at a
// commit of their own.
func text(def, sites string) string {
	return fmt.Sprintf("{\n  %q: %q,\n  %q: {%s}\n}\n", "default", def, "per_site", sites)
}

// The pins are a default and each site's own, every one a full commit hash,
// and a site with no pin of its own runs the default.
func TestParseReadsTheDefaultAndEachSitesOwn(t *testing.T) {
	p, err := Parse([]byte(text(shaA, `"site7": "`+shaB+`"`)))
	if err != nil {
		t.Fatal(err)
	}
	if p.For("site7") != shaB || p.For("site8") != shaA {
		t.Errorf("site7 runs %s and site8 runs %s", p.For("site7"), p.For("site8"))
	}
	for name, body := range map[string]string{
		"a branch where a commit goes": text("main", ""),
		"seven characters of a hash":   text("aaaaaaa", ""),
		"no default":                   `{"per_site": {}}`,
		"a site on a tag":              text(shaA, `"site7": "v1"`),
		"a field nothing reads":        strings.Replace(text(shaA, ""), "\n}", ",\n  \"extra\": 1\n}", 1),
		"not JSON":                     `default: x`,
	} {
		if _, err := Parse([]byte(body)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}

	root := t.TempDir()
	if _, err := Read(root); err == nil {
		t.Error("a repository with no pins file was read")
	}
	path := filepath.Join(root, filepath.FromSlash(File))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text(shaA, "")), 0o600); err != nil {
		t.Fatal(err)
	}
	if p, err := Read(root); err != nil || p.Default != shaA {
		t.Errorf("read as %+v, %v", p, err)
	}
}
