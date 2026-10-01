package tofufiles

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Read lists what is tracked, so the tree is a repository with these
	// files added to it and nothing committed.
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return root
}

// Every authored file is read, wherever it is, and nothing that is a cache, a
// scratch copy or a fixture.
func TestReadFindsEveryAuthoredFileAndNothingElse(t *testing.T) {
	root := tree(t, map[string]string{
		"roots/one/a.tf":       "a",
		"roots/two/b.tf":       "b",
		"shared/thing/c.tf":    "c",
		"roots/one/tests/e.tf": "fixture",
		"roots/one/readme.md":  "not tofu",
	})
	// On disk and not tracked: a provider cache, which is not the
	// repository's.
	cache := filepath.Join(root, "roots", "one", "cache", "d.tf")
	if err := os.MkdirAll(filepath.Dir(cache), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cache, []byte("cache"), 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 || files["roots/one/a.tf"] != "a" || files["roots/two/b.tf"] != "b" || files["shared/thing/c.tf"] != "c" {
		t.Errorf("read %v", files)
	}
	if _, err := Read(tree(t, map[string]string{"readme.md": "x"})); err == nil {
		t.Error("a tree with no OpenTofu in it was read as an empty set rather than refused")
	}
	if _, err := Read(filepath.Join(root, "absent")); err == nil {
		t.Error("a directory that does not exist was read")
	}
}

// A declaration is found in whichever file holds it, a comment describing one
// is not a declaration, and none or several is an error that says which.
func TestDeclaringFindsTheOneFileWhereverItIs(t *testing.T) {
	files := map[string]string{
		"roots/one/a.tf": "resource \"x\" \"here\" {}\n# resource \"x\" \"described\" {}\n  // resource \"x\" \"also_described\" {}\n",
		"roots/two/b.tf": "resource \"x\" \"there\" {}\nlocals {\n  shared = 1\n}\n",
		"roots/two/c.tf": "locals {\n  shared = 2\n}\n",
	}
	path, body, err := Declaring(files, `resource "x" "there"`)
	if err != nil || path != "roots/two/b.tf" || body != files[path] {
		t.Errorf("got %q, %v", path, err)
	}
	// However its assignments are aligned.
	aligned := map[string]string{"roots/one/a.tf": "locals {\n  first      = 1\n  much_longer = 2\n}\n"}
	if path, _, err := Declaring(aligned, "first = 1"); err != nil || path != "roots/one/a.tf" {
		t.Errorf("an aligned assignment was not found: %q, %v", path, err)
	}
	for decl, want := range map[string]string{
		`resource "x" "gone"`:           "no OpenTofu file declares",
		`resource "x" "described"`:      "no OpenTofu file declares",
		`resource "x" "also_described"`: "no OpenTofu file declares",
		`shared =`:                      "roots/two/b.tf, roots/two/c.tf",
	} {
		if _, _, err := Declaring(files, decl); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want an error saying %q", decl, err, want)
		}
	}
	if got := In(files, "roots/two"); len(got) != 2 || got["roots/two/c.tf"] == "" {
		t.Errorf("the files of one directory are %v", got)
	}
}
