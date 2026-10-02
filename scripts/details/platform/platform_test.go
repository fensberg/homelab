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

const aDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// A site's line is a version and the digest it was published as, and nothing
// else is one: a version alone can be moved, a digest alone says nothing to a
// person, and a tag such as latest is neither.
func TestParsePinTakesAVersionAndItsDigest(t *testing.T) {
	pin, err := ParsePin("v2026.10.1@" + aDigest)
	if err != nil || pin.Version != "v2026.10.1" || pin.Digest != aDigest {
		t.Fatalf("read as %+v, %v", pin, err)
	}
	for _, bad := range []string{"v2026.10.1", aDigest, "latest@" + aDigest, "v2026.10.1@sha256:abc", "v2026.01.1@" + aDigest, "v2026.10.1@" + aDigest + "0", " v2026.10.1@" + aDigest, ""} {
		if pin, err := ParsePin(bad); err == nil {
			t.Errorf("%q was read as %+v", bad, pin)
		}
	}
}

// Each site runs the release its own line names, and a site with no line
// runs nothing rather than something chosen for it.
func TestPinnedIsTheSitesOwnLine(t *testing.T) {
	root := t.TempDir()
	write := func(body string) {
		path := filepath.Join(root, filepath.FromSlash(VersionsFile))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Pinned(root, "site7"); err == nil {
		t.Error("a repository with no versions file gave a site a release")
	}
	other := strings.Replace(aDigest, "0123", "ffff", 1)
	write(`{"site7": {"platform": "v2026.10.1@` + aDigest + `"}, "site8": {"platform": "v2026.11.3@` + other + `"}}`)
	if pin, err := Pinned(root, "site7"); err != nil || pin != (Pin{"v2026.10.1", aDigest}) {
		t.Errorf("site7 runs %+v, %v", pin, err)
	}
	if pin, err := Pinned(root, "site8"); err != nil || pin != (Pin{"v2026.11.3", other}) {
		t.Errorf("site8 runs %+v, %v", pin, err)
	}
	if pin, err := Pinned(root, "site9"); err == nil || !strings.Contains(err.Error(), "says nothing of site9") {
		t.Errorf("a site with no line runs %+v, %v", pin, err)
	}
	for name, body := range map[string]string{
		"a version with no digest": `{"site7": {"platform": "v2026.10.1"}}`,
		"a field nothing reads":    `{"site7": {"platform": "v2026.10.1@` + aDigest + `", "also": 1}}`,
		"not JSON":                 `site7: v2026.10.1`,
	} {
		write(body)
		if pin, err := Pinned(root, "site7"); err == nil {
			t.Errorf("%s: read as %+v", name, pin)
		}
	}
}

// The repository's own versions file reads, and names a release for every
// site it mentions.
func TestTheRepositorysVersionsRead(t *testing.T) {
	root, err := repopath.Root()
	if err != nil {
		t.Fatal(err)
	}
	pins, err := Pins(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(pins) == 0 {
		t.Errorf("%s names no site, so no site runs anything", VersionsFile)
	}
}

// Releases are published beside the repository, under the platform's name,
// in lower case whatever the repository's own spelling.
func TestRegistryIsTheRepositorysPlatformRelease(t *testing.T) {
	if got := Registry("Example/HomeLab"); got != RegistryHost+"/example/homelab-"+Order+"-release" {
		t.Errorf("got %s", got)
	}
}

// The settings tofu fetches a release with hold the token as the registry's
// password and nothing else; and what is not a token is refused rather than
// written into a file tofu would then parse.
func TestCLIConfigHoldsTheRegistrysCredential(t *testing.T) {
	got, err := CLIConfig(" a-token_123 \n")
	if err != nil {
		t.Fatal(err)
	}
	want := "oci_credentials \"" + RegistryHost + "\" {\n  username = \"x-access-token\"\n  password = \"a-token_123\"\n}\n"
	if string(got) != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	for name, bad := range map[string]string{"nothing": "", "only space": " \n", "a quote": `to"ken`, "a backslash": `to\ken`, "two lines": "to\nken"} {
		if got, err := CLIConfig(bad); err == nil {
			t.Errorf("%s was written as %q", name, got)
		}
	}
}

// The unreleased tree holds what a release of the platform would - the
// tracked files its manifest names, as they are in the checkout - and nothing
// else of the repository, whatever was there before.
func TestPlaceUnreleasedHoldsOnlyWhatTheManifestNames(t *testing.T) {
	root := t.TempDir()
	for rel, body := range map[string]string{
		Manifest:                      `{"holds": ["` + ModulesDir + `", "ground/read.yaml"]}`,
		ModulesDir + "/thing/main.tf": "# as it is in the checkout\n",
		"ground/read.yaml":            "a: 1\n",
		"ground/beside.yaml":          "not held\n",
		"leaflets/words.md":           "not held\n",
	} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tracked := []string{Manifest, ModulesDir + "/thing/main.tf", "ground/read.yaml", "ground/beside.yaml", "leaflets/words.md"}

	// Left from an earlier placing, and not in the release now.
	stale := filepath.Join(root, Unreleased, "left", "behind.txt")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	target, err := PlaceUnreleased(root, tracked)
	if err != nil {
		t.Fatal(err)
	}
	var placed []string
	err = filepath.WalkDir(target, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(target, p)
		if err != nil {
			return err
		}
		placed = append(placed, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ground/read.yaml", Manifest, ModulesDir + "/thing/main.tf"}
	if strings.Join(placed, "\n") != strings.Join(want, "\n") {
		t.Errorf("the unreleased tree holds\n  %s\nwant exactly\n  %s", strings.Join(placed, "\n  "), strings.Join(want, "\n  "))
	}
	if got, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(ModulesDir), "thing", "main.tf")); err != nil || string(got) != "# as it is in the checkout\n" {
		t.Errorf("a held file was placed as %q, %v", got, err)
	}
	if _, err := PlaceUnreleased(t.TempDir(), tracked); err == nil {
		t.Error("a tree was placed with no manifest to say what it holds")
	}
}
