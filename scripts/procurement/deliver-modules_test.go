package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"homelab/details/pins"
)

// moduleRepo is a repository with a module that reads one file outside
// itself, a pins file, and files no site reads.
func moduleRepo(t *testing.T) (root string, git func(args ...string) string, commit func(msg string, files map[string]string) string) {
	t.Helper()
	root = t.TempDir()
	git = func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.email=a@example.com", "-c", "user.name=t"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	commit = func(msg string, files map[string]string) string {
		t.Helper()
		for rel, body := range files {
			path := filepath.Join(root, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		git("add", "-A")
		git("commit", "-qm", msg)
		return git("rev-parse", "HEAD")
	}
	return root, git, commit
}

func runGit(root string) pins.Git {
	return func(args ...string) ([]byte, error) {
		return exec.Command("git", append([]string{"-C", root}, args...)...).Output()
	}
}

// The default moves to the merged commit when the modules, or a file they
// read, changed since the pin - and stays where it is when what changed is
// something no site reads. Against a real repository, so the reading of the
// modules and of both commits is exercised and not only the rule.
func TestTheDefaultPinMovesWhenWhatASiteReadsChanged(t *testing.T) {
	root, _, commit := moduleRepo(t)
	module := pins.ModulesDir + "/thing/main.tf"
	pinned := commit("base", map[string]string{
		module:              "x = file(\"${path.module}/../../../ground/read.yaml\")\n",
		"ground/read.yaml":  "a: 1\n",
		"ground/other.yaml": "b: 1\n",
		"docs/readme.md":    "one\n",
	})
	file := filepath.Join(t.TempDir(), "pins.json")
	write := func() {
		if err := os.WriteFile(file, []byte("{\n  \"default\": \""+pinned+"\",\n  \"per_site\": {}\n}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	defaultNow := func() string {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		p, err := pins.Parse(body)
		if err != nil {
			t.Fatal(err)
		}
		return p.Default
	}
	for _, c := range []struct {
		name     string
		files    map[string]string
		wantMove bool
		wantSay  string
	}{
		{"prose", map[string]string{"docs/readme.md": "two\n"}, false, "nothing a site reads differs"},
		{"a file beside the one a module reads", map[string]string{"ground/other.yaml": "b: 2\n"}, false, "nothing a site reads differs"},
		{"a module", map[string]string{module: "x = file(\"${path.module}/../../../ground/read.yaml\")\ny = 1\n"}, true, module},
		{"the file a module reads", map[string]string{"ground/read.yaml": "a: 2\n"}, true, "ground/read.yaml"},
	} {
		write()
		merged := commit(c.name, c.files)
		said, err := moveDefaultPin(root, file, merged, runGit(root))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if moved := defaultNow() == merged; moved != c.wantMove || !strings.Contains(said, c.wantSay) {
			t.Errorf("%s: moved=%v, want %v; said %q", c.name, moved, c.wantMove, said)
		}
		if !c.wantMove && defaultNow() != pinned {
			t.Errorf("%s: the file was changed although the pin stays", c.name)
		}
		// Each case starts from the pin again, with the change undone.
		commit("undo "+c.name, map[string]string{
			module: "x = file(\"${path.module}/../../../ground/read.yaml\")\n", "ground/read.yaml": "a: 1\n",
			"ground/other.yaml": "b: 1\n", "docs/readme.md": "one\n",
		})
	}

	// Already there: nothing to do, and the file is not rewritten.
	write()
	if said, err := moveDefaultPin(root, file, pinned, runGit(root)); err != nil || !strings.Contains(said, "already") || defaultNow() != pinned {
		t.Errorf("asked to move the pin to where it is: %q, %v", said, err)
	}
}

// What cannot be answered is an error, and the file is left as it was: a
// commit that is not one, a commit nobody holds, a pins file that does not
// read, and a module whose reach cannot be followed.
func TestThePinIsNotMovedOnAnAnswerItCouldNotGet(t *testing.T) {
	root, _, commit := moduleRepo(t)
	module := pins.ModulesDir + "/thing/main.tf"
	pinned := commit("base", map[string]string{module: "x = 1\n"})
	merged := commit("next", map[string]string{module: "x = 2\n"})
	file := filepath.Join(t.TempDir(), "pins.json")
	good := "{\n  \"default\": \"" + pinned + "\",\n  \"per_site\": {}\n}\n"
	absent := strings.Repeat("c", 40)
	for name, c := range map[string]struct{ body, to string }{
		"a branch name":            {good, "main"},
		"a commit nobody holds":    {good, absent},
		"a pin nobody holds":       {strings.Replace(good, pinned, absent, 1), merged},
		"a pins file not readable": {`{"default": "main"}`, merged},
	} {
		if err := os.WriteFile(file, []byte(c.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if said, err := moveDefaultPin(root, file, c.to, runGit(root)); err == nil {
			t.Errorf("%s: %q", name, said)
		}
		if after, _ := os.ReadFile(file); string(after) != c.body {
			t.Errorf("%s: the file was changed", name)
		}
	}
	unfollowable := commit("a reach through a value", map[string]string{module: "x = \"${path.module}/../../../${local.where}/f\"\n"})
	if err := os.WriteFile(file, []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	if said, err := moveDefaultPin(root, file, unfollowable, runGit(root)); err == nil || !strings.Contains(err.Error(), "reaches outside its module") {
		t.Errorf("a module whose reach cannot be followed: %q, %v", said, err)
	}
	if said, err := moveDefaultPin(root, filepath.Join(t.TempDir(), "absent.json"), merged, runGit(root)); err == nil {
		t.Errorf("a pins file that is not there: %q", said)
	}
}

// The verb itself: it needs both flags, prints what it did, and exits
// non-zero when it could not tell.
func TestTheDeliverModulesVerb(t *testing.T) {
	root, _, commit := moduleRepo(t)
	module := pins.ModulesDir + "/thing/main.tf"
	pinned := commit("base", map[string]string{module: "x = 1\n"})
	merged := commit("next", map[string]string{module: "x = 2\n"})
	file := filepath.Join(t.TempDir(), "pins.json")
	if err := os.WriteFile(file, []byte("{\n  \"default\": \""+pinned+"\",\n  \"per_site\": {}\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := deliverModules([]string{"-pins", file}); code != 2 {
		t.Errorf("with no commit to move to it exited %d, want 2", code)
	}
	if code := deliverModules([]string{"-no-such-flag"}); code != 2 {
		t.Errorf("a flag it does not have exited %d, want 2", code)
	}
	if code := deliverModules([]string{"-pins", file, "-to", "main", "-root", root}); code != 1 {
		t.Errorf("a commit that is not one exited %d, want 1", code)
	}
	out := captureStdout(t, func() {
		if code := deliverModules([]string{"-pins", file, "-to", merged, "-root", root}); code != 0 {
			t.Errorf("exited %d", code)
		}
	})
	body, _ := os.ReadFile(file)
	if !strings.Contains(out, "moved the default pin from "+pinned[:7]+" to "+merged[:7]) || !strings.Contains(string(body), merged) {
		t.Errorf("printed %q and left the file as:\n%s", out, body)
	}
}
