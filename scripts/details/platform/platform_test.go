package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homelab/details/repopath"
	"homelab/details/tofufiles"
)

// What the modules are read with is the modules and every path a module
// names outside its own directory - a file, a directory or a pattern - and
// nothing a root, a test or a comment names. A file inside a directory that
// is itself read is not listed twice.
func TestReadsIsWhatTheModulesReachFor(t *testing.T) {
	files := map[string]string{
		ModulesDir + "/one/main.tf":      "x = file(\"${path.module}/../../../ground/thing.yaml\")\ny = \"${path.module}/../../../ground/apps/*/declared.json\"\n# z = \"${path.module}/../../../commented/out\"\n",
		ModulesDir + "/one/deep/more.tf": "d = \"${path.module}/../../../../other/dir\"\ne = file(\"${path.module}/../../../../other/dir/inside.txt\")\ninside = \"${path.module}/files/local.txt\"\n",
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
		"the repository's top":  {"x = fileset(\"${path.module}/../../..\", \"**\")\n", "the whole repository"},
		"the top, with a slash": {"x = trimprefix(y, \"${path.module}/../../../\")\n", "the whole repository"},
	} {
		_, err := Reads(map[string]string{ModulesDir + "/one/main.tf": c.body})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// A release holds a file when a path it names is that file, a directory
// above it, or a pattern it matches - a pattern's * standing for one name,
// never for a path.
func TestHoldsIsAFileADirectoryOrAPattern(t *testing.T) {
	holds := []string{"ground/thing.yaml", "ground/dir", "ground/apps/*/declared.json"}
	for file, want := range map[string]bool{
		"ground/thing.yaml":                true,
		"ground/thing.yaml.bak":            false,
		"ground/dir/a.txt":                 true,
		"ground/dir/deep/b.txt":            true,
		"ground/directory/c.txt":           false,
		"ground/apps/a/declared.json":      true,
		"ground/apps/a/manifests/x.yaml":   false,
		"ground/apps/a/deep/declared.json": false,
		"elsewhere/thing.yaml":             false,
	} {
		if got := Holds(holds, file); got != want {
			t.Errorf("%s: held=%v, want %v", file, got, want)
		}
	}
}

// A version is the year, the month with no leading zero, and a number that
// starts at one; and nothing else is one.
func TestVersionIsYearMonthAndNumber(t *testing.T) {
	for _, ok := range []string{"v2026.10.1", "v2026.1.1", "v2027.12.40"} {
		if !Version.MatchString(ok) {
			t.Errorf("%s was refused", ok)
		}
	}
	for _, bad := range []string{"2026.10.1", "v2026.01.1", "v2026.13.1", "v2026.0.1", "v2026.10.0", "v2026.10", "v26.10.1", "v2026.10.1-2", "latest", "v2026.10.01"} {
		if Version.MatchString(bad) {
			t.Errorf("%s was accepted", bad)
		}
	}
}

// The manifest is read whole, and one that holds nothing or carries a field
// nothing reads is refused.
func TestReadRefusesAManifestThatSaysNothing(t *testing.T) {
	for name, c := range map[string]struct {
		body string
		ok   bool
	}{
		"what it holds":         {`{"_comment": ["x"], "holds": ["a", "b/*"]}`, true},
		"holding nothing":       {`{"holds": []}`, false},
		"an empty manifest":     {`{}`, false},
		"a field nothing reads": {`{"holds": ["a"], "also": 1}`, false},
		"not JSON":              {`holds: a`, false},
	} {
		root := t.TempDir()
		path := filepath.Join(root, filepath.FromSlash(Manifest))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(c.body), 0o600); err != nil {
			t.Fatal(err)
		}
		r, err := Read(root)
		if (err == nil) != c.ok {
			t.Errorf("%s: %+v, %v", name, r, err)
		}
		if c.ok && len(r.Holds) != 2 {
			t.Errorf("%s: read as %+v", name, r)
		}
	}
	if _, err := Read(t.TempDir()); err == nil {
		t.Error("a repository with no manifest was read")
	}
}

// The repository's manifest holds exactly what its modules reach for: a path
// a module reads and the manifest lacks would be missing from every release,
// and one the manifest names and nothing reads is carried for no reason.
func TestTheManifestHoldsExactlyWhatTheModulesReachFor(t *testing.T) {
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
	release, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(release.Holds, "\n") != strings.Join(reads, "\n") {
		t.Errorf("%s holds\n  %s\nand the modules reach for\n  %s\n\nA release is packed from the manifest, so it must name exactly what the modules read, in order.",
			Manifest, strings.Join(release.Holds, "\n  "), strings.Join(reads, "\n  "))
	}
	// And each names something that is there. A pattern over applications
	// matches nothing in an estate that has none; a path with no pattern has
	// to exist.
	for _, held := range release.Holds {
		found, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(held)))
		if (err != nil || len(found) == 0) && !strings.ContainsAny(held, "*?[") {
			t.Errorf("the manifest holds %s, which is not in the repository (%v)", held, err)
		}
	}
}

// The files a release holds are the tracked files its manifest names, in
// order, and a manifest that holds none of them is refused.
func TestFilesIsTheTrackedFilesTheManifestHolds(t *testing.T) {
	root := t.TempDir()
	write := func(body string) {
		path := filepath.Join(root, filepath.FromSlash(Manifest))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tracked := []string{"ground/thing.yaml", "mods/b.tf", "notes/prose.md", "mods/a.tf", "ground/other.yaml"}
	write(`{"holds": ["mods", "ground/thing.yaml"]}`)
	got, err := Files(root, tracked)
	if err != nil || strings.Join(got, ",") != "ground/thing.yaml,mods/a.tf,mods/b.tf" {
		t.Errorf("got %v, %v", got, err)
	}
	write(`{"holds": ["nowhere"]}`)
	if got, err := Files(root, tracked); err == nil {
		t.Errorf("a manifest holding nothing that is tracked gave %v", got)
	}
	if _, err := Files(t.TempDir(), tracked); err == nil {
		t.Error("a repository with no manifest gave a release")
	}
}
