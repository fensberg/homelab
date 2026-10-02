package pin

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homelab/details/platform"
)

// repo is a repository with two commits, each holding a module file that
// says which it is, and the hashes of both.
func repo(t *testing.T) (root, first, second string) {
	t.Helper()
	root = t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := Exec(root, args...)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(rel, body string, mode os.FileMode) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	git("config", "user.email", "test@example.invalid")
	git("config", "user.name", "test")
	write("parts/thing/main.tf", "# first\n", 0o644)
	write("parts/thing/run.sh", "#!/bin/sh\n", 0o755)
	write("routes.json", "{}\n", 0o644)
	git("add", "-A")
	git("commit", "-q", "-m", "first")
	first = git("rev-parse", "HEAD")
	write("parts/thing/main.tf", "# second\n", 0o644)
	git("add", "-A")
	git("commit", "-q", "-m", "second")
	second = git("rev-parse", "HEAD")
	return root, first, second
}

func pins(t *testing.T, root, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(File))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A site with no pin of its own runs the default; one with a pin runs that;
// and each is handed the whole tree as it was at its commit.
func TestASiteIsHandedTheTreeAtItsPinOrTheDefault(t *testing.T) {
	root, first, second := repo(t)
	pins(t, root, `{"default": "`+second+`", "per_site": {"held": "`+first+`"}}`)

	for site, want := range map[string]struct{ sha, body string }{
		"anyone": {second, "# second\n"},
		"held":   {first, "# first\n"},
	} {
		sha, err := Place(root, site, Exec)
		if err != nil {
			t.Fatal(err)
		}
		if sha != want.sha {
			t.Errorf("%s runs %s, want %s", site, sha, want.sha)
		}
		tree := Dir(root, site)
		if got := read(t, filepath.Join(tree, "parts", "thing", "main.tf")); got != want.body {
			t.Errorf("%s was handed %q, want %q", site, got, want.body)
		}
		// The whole tree, since a module reads files beside the modules.
		if _, err := os.Stat(filepath.Join(tree, "routes.json")); err != nil {
			t.Errorf("%s was not handed the files a module reads: %v", site, err)
		}
		if info, err := os.Stat(filepath.Join(tree, "parts", "thing", "run.sh")); err != nil || info.Mode()&0o100 == 0 {
			t.Errorf("a script in %s's tree is no longer executable", site)
		}
	}
}

// Moving a pin replaces the tree, a tree already of the pinned commit is left
// alone, and the working tree a check reads is never a site's.
func TestMovingAPinReplacesTheTreeAndTheWorkingTreeIsNeverASites(t *testing.T) {
	root, first, second := repo(t)
	pins(t, root, `{"default": "`+first+`", "per_site": {}}`)
	if _, err := Place(root, "site0", Exec); err != nil {
		t.Fatal(err)
	}
	module := filepath.Join(Dir(root, "site0"), "parts", "thing", "main.tf")

	// Left alone: git is not asked for the tree a second time.
	asked := 0
	counting := func(r string, args ...string) ([]byte, error) {
		asked++
		return Exec(r, args...)
	}
	if _, err := Place(root, "site0", counting); err != nil || asked != 0 {
		t.Errorf("a tree already of the pinned commit was read again (%d git call(s), %v)", asked, err)
	}

	pins(t, root, `{"default": "`+second+`", "per_site": {}}`)
	if _, err := Place(root, "site0", Exec); err != nil {
		t.Fatal(err)
	}
	if got := read(t, module); got != "# second\n" {
		t.Errorf("after the pin moved the site still runs %q", got)
	}

	// A check's link to the working tree shows an edit nobody has committed,
	// and is a tree of its own: the site's stays what its pin says.
	if err := PlaceWorkingTree(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "parts", "thing", "main.tf"), []byte("# uncommitted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(Dir(root, WorkingTree), "parts", "thing", "main.tf")); got != "# uncommitted\n" {
		t.Errorf("the working-tree link does not show the working tree: %q", got)
	}
	if got := read(t, module); got != "# second\n" {
		t.Errorf("a check's link changed what the site runs: %q", got)
	}
	// Made again, it is still one link and not a link inside the last.
	if err := PlaceWorkingTree(root); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(Dir(root, WorkingTree)); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the working tree is not a link: %v", err)
	}
	// And no site can be given the working tree's name.
	if _, err := Place(root, WorkingTree, Exec); err == nil {
		t.Error("a site was pinned under the working tree's name, so a check would have replaced its modules")
	}
}

// A commit the checkout does not hold is fetched by its hash, and one that
// cannot be had is an error naming the site and the commit.
func TestACommitTheCheckoutLacksIsFetchedOrRefused(t *testing.T) {
	root, first, _ := repo(t)
	missing := strings.Repeat("ab", 20)
	pins(t, root, `{"default": "`+missing+`", "per_site": {}}`)

	var fetched []string
	git := func(r string, args ...string) ([]byte, error) {
		switch {
		case args[0] == "fetch":
			fetched = args
			return nil, nil
		case args[0] == "archive":
			// Once fetched, the commit is there to read.
			return Exec(r, "archive", "--format=tar", first)
		}
		return Exec(r, args...)
	}
	if _, err := Place(root, "site0", git); err != nil {
		t.Fatal(err)
	}
	if len(fetched) == 0 || fetched[len(fetched)-1] != missing || fetched[len(fetched)-2] != "origin" {
		t.Errorf("a missing commit was not fetched by its hash: %v", fetched)
	}

	_, err := Place(root, "another", Exec)
	if err == nil || !strings.Contains(err.Error(), "another") || !strings.Contains(err.Error(), missing) {
		t.Errorf("a pin to a commit that cannot be had: %v", err)
	}
	if _, statErr := os.Stat(Dir(root, "another")); statErr == nil {
		t.Error("a tree was left for a site whose pin could not be had")
	}
}

// A pin is a full commit hash. Anything else names whatever it points at
// today, and a file with no default leaves an unpinned site running nothing.
func TestPinsThatAreNotCommitsAreRefused(t *testing.T) {
	root := t.TempDir()
	good := strings.Repeat("0123abcdef", 4)
	for name, body := range map[string]string{
		"a branch as the default": `{"default": "main", "per_site": {}}`,
		"a short hash":            `{"default": "0123456", "per_site": {}}`,
		"no default":              `{"per_site": {"site0": "` + good + `"}}`,
		"a tag for a site":        `{"default": "` + good + `", "per_site": {"site0": "v1.2.3"}}`,
		"an upper-case hash":      `{"default": "` + strings.ToUpper(good) + `", "per_site": {}}`,
		"a key nothing reads":     `{"default": "` + good + `", "per_site": {}, "production": "` + good + `"}`,
		"not JSON":                `default: main`,
	} {
		pins(t, root, body)
		if _, err := Read(root); err == nil {
			t.Errorf("%s was read as a pin", name)
		}
	}
	pins(t, root, `{"default": "`+good+`", "per_site": {}}`)
	if p, err := Read(root); err != nil || p.For("anything") != good {
		t.Errorf("a file with only a default: %v, %v", p, err)
	}
	if _, err := Read(t.TempDir()); err == nil {
		t.Error("a repository with no pins was read as pinned")
	}
	if _, err := Place(t.TempDir(), "site0", Exec); err == nil {
		t.Error("a site was placed with no pins to place it by")
	}
}

// What is extracted is what a git tree holds, inside the tree.
func TestExtractRefusesAnythingOutsideTheTree(t *testing.T) {
	if err := extract([]byte("not a tar archive at all, and long enough to be read as a header block: "+strings.Repeat("x", 512)), filepath.Join(t.TempDir(), "x")); err == nil {
		t.Error("something that is not an archive was extracted")
	}
}

// The release tree holds what a release of the platform would - the tracked
// files its manifest names, as they are in the checkout - and nothing else
// of the repository, whatever was there before.
func TestPlaceReleaseHoldsOnlyWhatTheManifestNames(t *testing.T) {
	root := t.TempDir()
	for rel, body := range map[string]string{
		platform.Manifest:                      `{"holds": ["` + platform.ModulesDir + `", "ground/read.yaml"]}`,
		platform.ModulesDir + "/thing/main.tf": "# as it is in the checkout\n",
		"ground/read.yaml":                     "a: 1\n",
		"ground/beside.yaml":                   "not held\n",
		"leaflets/words.md":                    "not held\n",
	} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tracked := platform.Manifest + "\x00" + platform.ModulesDir + "/thing/main.tf\x00ground/read.yaml\x00ground/beside.yaml\x00leaflets/words.md\x00"
	git := func(string, ...string) ([]byte, error) { return []byte(tracked), nil }

	// Left from an earlier placing, and not in the release now.
	stale := filepath.Join(Dir(root, Release), "left", "behind.txt")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := PlaceRelease(root, git); err != nil {
		t.Fatal(err)
	}
	var placed []string
	err := filepath.WalkDir(Dir(root, Release), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(Dir(root, Release), p)
		if err != nil {
			return err
		}
		placed = append(placed, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ground/read.yaml", platform.Manifest, platform.ModulesDir + "/thing/main.tf"}
	if strings.Join(placed, "\n") != strings.Join(want, "\n") {
		t.Errorf("the release tree holds\n  %s\nwant exactly\n  %s", strings.Join(placed, "\n  "), strings.Join(want, "\n  "))
	}
	if got := read(t, filepath.Join(Dir(root, Release), filepath.FromSlash(platform.ModulesDir), "thing", "main.tf")); got != "# as it is in the checkout\n" {
		t.Errorf("a held file was placed as %q", got)
	}

	// A git that cannot list, and a manifest that is not there, place nothing.
	if err := PlaceRelease(root, func(string, ...string) ([]byte, error) { return nil, errors.New("not a repository") }); err == nil {
		t.Error("a release was placed from a checkout git could not list")
	}
	if err := PlaceRelease(t.TempDir(), git); err == nil {
		t.Error("a release was placed with no manifest to say what it holds")
	}
}
