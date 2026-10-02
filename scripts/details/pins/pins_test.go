package pins

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homelab/details/repopath"
	"homelab/details/tofufiles"
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

// What a pinned tree is read for is the modules and every path a module
// names outside its own directory - a file, a directory or a pattern - and
// nothing a root, a test or a comment names.
func TestReadsIsWhatTheModulesReachFor(t *testing.T) {
	files := map[string]string{
		ModulesDir + "/one/main.tf":      "x = file(\"${path.module}/../../../ground/thing.yaml\")\ny = \"${path.module}/../../../ground/apps/*/declared.json\"\n# z = \"${path.module}/../../../commented/out\"\n",
		ModulesDir + "/one/deep/more.tf": "d = \"${path.module}/../../../../other/dir\"\ninside = \"${path.module}/files/local.txt\"\n",
		ModulesDir + "/two/main.tf":      "sibling = \"${path.module}/../one\"\nagain = file(\"${path.module}/../../../ground/thing.yaml\")\n",
		ModulesDir + "/two/t.tftest.hcl": "t = \"${path.module}/../../../only/a/test\"\n",
		"ground/site/other.tf":           "r = \"${path.module}/../../not/a/module\"\n",
	}
	got, err := Reads(files)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ground/apps/*/declared.json", "ground/thing.yaml", ModulesDir, "other/dir"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got  %v\nwant %v", got, want)
	}

	for name, c := range map[string]struct{ body, want string }{
		"through a value":       {"x = \"${path.module}/../../../${local.where}/thing\"\n", "not written in the path"},
		"a value further along": {"x = \"${path.module}/../../../ground/${var.site}/thing\"\n", "not written in the path"},
		"out of the repository": {"x = \"${path.module}/../../../../../elsewhere\"\n", "outside the repository"},
	} {
		_, err := Reads(map[string]string{ModulesDir + "/one/main.tf": c.body})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// The repository's own modules are readable this way: every reach is a whole
// path, and each names something that is there.
func TestTheRepositorysModulesSayWhatTheyReach(t *testing.T) {
	root, err := repopath.Root()
	if err != nil {
		t.Fatal(err)
	}
	files, err := tofufiles.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	reads, err := Reads(files)
	if err != nil {
		t.Fatal(err)
	}
	if len(reads) < 2 {
		t.Fatalf("the modules are read as reaching nothing but themselves (%v), and they read the manifests Flux is started from", reads)
	}
	for _, read := range reads {
		found, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(read)))
		if err != nil || len(found) == 0 {
			// A pattern over applications matches nothing in an estate
			// that has none; a path with no pattern has to be there.
			if strings.ContainsAny(read, "*?[") {
				continue
			}
			t.Errorf("a module reaches %s, which is not in the repository (%v)", read, err)
		}
	}
}

// tree is a git that answers ls-tree for the commits a test describes.
func tree(commits map[string]map[string]string) Git {
	return func(args ...string) ([]byte, error) {
		if len(args) != 4 || args[0] != "ls-tree" {
			return nil, fmt.Errorf("unexpected git %v", args)
		}
		files, ok := commits[args[3]]
		if !ok {
			return nil, errors.New("no such commit")
		}
		var out strings.Builder
		for file, hash := range files {
			fmt.Fprintf(&out, "100644 blob %s\t%s\n", hash, file)
		}
		return []byte(out.String()), nil
	}
}

// Two commits differ, for a site, when a file its tree is read for was
// changed, added or removed - and in nothing else.
func TestChangedSeesOnlyWhatAPinnedTreeIsReadFor(t *testing.T) {
	reads := []string{"ground/apps/*/declared.json", "ground/thing.yaml", ModulesDir}
	base := map[string]string{
		ModulesDir + "/one/main.tf":      "h1",
		"ground/thing.yaml":              "h2",
		"ground/apps/a/declared.json":    "h3",
		"ground/apps/a/manifests/x.yaml": "h4",
		"notes/prose.md":                 "h5",
	}
	with := func(change func(map[string]string)) map[string]string {
		out := map[string]string{}
		for k, v := range base {
			out[k] = v
		}
		change(out)
		return out
	}
	for name, c := range map[string]struct {
		to   map[string]string
		want string
	}{
		"the same tree":                   {with(func(map[string]string) {}), ""},
		"a file nothing reads":            {with(func(m map[string]string) { m["notes/prose.md"] = "x" }), ""},
		"an application's manifests":      {with(func(m map[string]string) { m["ground/apps/a/manifests/x.yaml"] = "x" }), ""},
		"a file beside one that is read":  {with(func(m map[string]string) { m["ground/thing.yaml.bak"] = "x" }), ""},
		"a declaration two levels down":   {with(func(m map[string]string) { m["ground/apps/a/deep/declared.json"] = "x" }), ""},
		"a module":                        {with(func(m map[string]string) { m[ModulesDir+"/one/main.tf"] = "x" }), ModulesDir + "/one/main.tf"},
		"a new file in a module":          {with(func(m map[string]string) { m[ModulesDir+"/two/new.tf"] = "x" }), ModulesDir + "/two/new.tf"},
		"a file a module reads":           {with(func(m map[string]string) { m["ground/thing.yaml"] = "x" }), "ground/thing.yaml"},
		"an application's declaration":    {with(func(m map[string]string) { m["ground/apps/a/declared.json"] = "x" }), "ground/apps/a/declared.json"},
		"a new application's declaration": {with(func(m map[string]string) { m["ground/apps/b/declared.json"] = "x" }), "ground/apps/b/declared.json"},
		"a declaration taken away":        {with(func(m map[string]string) { delete(m, "ground/apps/a/declared.json") }), "ground/apps/a/declared.json"},
	} {
		got, err := Changed(tree(map[string]map[string]string{shaA: base, shaB: c.to}), shaA, shaB, reads)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if strings.Join(got, ",") != c.want {
			t.Errorf("%s: differs in %v, want %q", name, got, c.want)
		}
	}

	// A commit that cannot be listed, or holds nothing a site reads, is an
	// error and never "nothing changed".
	if _, err := Changed(tree(map[string]map[string]string{shaA: base}), shaA, shaB, reads); err == nil {
		t.Error("a commit that could not be listed was compared")
	}
	if _, err := Changed(tree(map[string]map[string]string{shaA: base}), shaB, shaA, reads); err == nil {
		t.Error("a pin that could not be listed was compared")
	}
	if _, err := Changed(tree(map[string]map[string]string{shaA: base, shaB: {"notes/prose.md": "h"}}), shaA, shaB, reads); err == nil {
		t.Error("a commit holding nothing a site reads was compared")
	}
}

// Moving the default changes that one line and nothing else, in the
// repository's own file and in one with a site held at its own commit.
func TestMoveDefaultChangesOneLine(t *testing.T) {
	root, err := repopath.Root()
	if err != nil {
		t.Fatal(err)
	}
	real, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(File)))
	if err != nil {
		t.Fatal(err)
	}
	held := text(shaA, "\n    \"site7\": \""+shaA+"\"\n  ")
	for name, before := range map[string]string{"the repository's": string(real), "one with a site held": held} {
		after, err := MoveDefault([]byte(before), shaB)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		b, a := strings.Split(before, "\n"), strings.Split(string(after), "\n")
		changed := 0
		for i := range b {
			if len(a) != len(b) || a[i] != b[i] {
				changed++
			}
		}
		p, err := Parse(after)
		if changed != 1 || err != nil || p.Default != shaB {
			t.Errorf("%s: %d line(s) changed, default %s, %v:\n%s", name, changed, p.Default, err, after)
		}
		if name == "one with a site held" && p.For("site7") != shaA {
			t.Errorf("moving the default moved a site held at its own commit:\n%s", after)
		}
	}
	for name, c := range map[string]struct{ body, sha string }{
		"a commit that is not one":            {held, "main"},
		"a file with no default":              {`{"per_site": {}}`, shaB},
		"a default on one line with the rest": {strings.ReplaceAll(text(shaA, ""), "\n", " "), shaB},
	} {
		if _, err := MoveDefault([]byte(c.body), c.sha); err == nil {
			t.Errorf("%s was moved", name)
		}
	}
}
