package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A repository built by the test, stating every input it depends on.
//
// GIT_CONFIG_GLOBAL and GIT_CONFIG_SYSTEM are neutralised because this shells
// out to git, and a developer's own config has already broken tests in this
// repository twice - once by signing a fixture that was meant to be unsigned.
// A test that reads the machine reports the machine.
func fixture(t *testing.T, commits ...map[string]string) (root string, shas []string) {
	t.Helper()
	root = t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")

	for _, files := range commits {
		// A commit describes the whole tree, so anything absent is deleted.
		entries, _ := filepath.Glob(filepath.Join(root, "*"))
		for _, e := range entries {
			if filepath.Base(e) != ".git" {
				_ = os.RemoveAll(e)
			}
		}
		for name, body := range files {
			full := filepath.Join(root, name)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		run("add", "-A")
		run("commit", "-qm", "fixture", "--allow-empty")
		out, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
		if err != nil {
			t.Fatal(err)
		}
		shas = append(shas, strings.TrimSpace(string(out)))
	}
	return root, shas
}

const twoGuards = `package repo

import "testing"

func TestVersionsAreDeclaredOnce(t *testing.T) {
	t.Error("no")
}

func TestRunnerImageCarriesEveryBinary(t *testing.T) {
	t.Fatal("no")
}
`

const oneGuard = `package repo

import "testing"

func TestVersionsAreDeclaredOnce(t *testing.T) {
	t.Error("no")
}
`

// The case this exists for.
//
// A file overwritten in place, still compiling, still holding a test - so
// nothing obvious breaks and the diff is a wall of additions elsewhere. The
// only thing that says a guard went missing is a count of what went missing.
func TestARemovedGuardIsNamed(t *testing.T) {
	root, shas := fixture(t,
		map[string]string{"tests/go/repo/a_test.go": twoGuards},
		map[string]string{"tests/go/repo/a_test.go": oneGuard},
	)

	r, err := take(root, shas[0], shas[1])
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if len(r.tests) != 1 {
		t.Fatalf("found %d removed tests, want 1: %v", len(r.tests), r.tests)
	}
	if _, ok := r.tests["TestRunnerImageCarriesEveryBinary"]; !ok {
		t.Errorf("the removed guard was not named: %v", r.tests)
	}

	out := render(r)
	if !strings.Contains(out, "TestRunnerImageCarriesEveryBinary") {
		t.Error("the report does not name the removed guard")
	}
	if !strings.Contains(out, "1 test function(s) removed") {
		t.Errorf("the report does not count it:\n%s", out)
	}
}

// A whole file deleted under a watched path is worth saying, separately from
// the tests that went with it.
func TestADeletedFileUnderAWatchedPathIsListed(t *testing.T) {
	root, shas := fixture(t,
		map[string]string{"tests/go/repo/a_test.go": twoGuards, "docs/note.md": "x"},
		map[string]string{"docs/note.md": "x"},
	)

	r, err := take(root, shas[0], shas[1])
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if len(r.files) != 1 || r.files[0] != "tests/go/repo/a_test.go" {
		t.Errorf("watched deletions are %v, want the test file", r.files)
	}
	if len(r.tests) != 2 {
		t.Errorf("found %d removed tests, want both that were in the file", len(r.tests))
	}
}

// Deleting a document is ordinary and is not worth an alarm.
func TestADeletedDocumentIsNotListed(t *testing.T) {
	root, shas := fixture(t,
		map[string]string{"docs/note.md": "x", "scripts/keep.go": "package main\n"},
		map[string]string{"scripts/keep.go": "package main\n"},
	)

	r, err := take(root, shas[0], shas[1])
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if len(r.files) != 0 {
		t.Errorf("a document deletion was reported as watched: %v", r.files)
	}
}

// A test that keeps its name and loses its teeth still passes.
func TestAGuardGuttedInPlaceShowsAsFewerAssertions(t *testing.T) {
	const gutted = `package repo

import "testing"

func TestVersionsAreDeclaredOnce(t *testing.T) {
	_ = t
}

func TestRunnerImageCarriesEveryBinary(t *testing.T) {
	_ = t
}
`
	root, shas := fixture(t,
		map[string]string{"tests/go/repo/a_test.go": twoGuards},
		map[string]string{"tests/go/repo/a_test.go": gutted},
	)

	r, err := take(root, shas[0], shas[1])
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if len(r.tests) != 0 {
		t.Errorf("no test was removed, but %v was reported", r.tests)
	}
	if r.assertions["tests/go/repo"] != -2 {
		t.Errorf("assertion delta is %d, want -2", r.assertions["tests/go/repo"])
	}
	if !strings.Contains(render(r), "2 fewer") {
		t.Error("the report does not say the package lost assertions")
	}
}

// Adding tests is not a removal, and must not be reported as one.
//
// A report that cries about ordinary work is a report people stop reading.
func TestAddingTestsReportsNothing(t *testing.T) {
	root, shas := fixture(t,
		map[string]string{"tests/go/repo/a_test.go": oneGuard},
		map[string]string{"tests/go/repo/a_test.go": twoGuards},
	)

	r, err := take(root, shas[0], shas[1])
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if len(r.tests) != 0 || len(r.files) != 0 {
		t.Errorf("an addition was reported as a removal: %+v", r)
	}
	if !strings.Contains(render(r), "Nothing.") {
		t.Errorf("the report should say plainly that nothing went:\n%s", render(r))
	}
}

// A rename is a delete and an add, and the test that moved has not gone.
func TestARenamedFileDoesNotLookLikeALostGuard(t *testing.T) {
	root, shas := fixture(t,
		map[string]string{"tests/go/repo/a_test.go": twoGuards},
		map[string]string{"tests/go/repo/b_test.go": twoGuards},
	)

	r, err := take(root, shas[0], shas[1])
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if len(r.tests) != 0 {
		t.Errorf("a rename reported %v as removed; both tests still exist", r.tests)
	}
}

// Could-not-compare must not read as nothing-was-removed.
func TestAnUncomparableRangeIsAnError(t *testing.T) {
	root, shas := fixture(t, map[string]string{"a.go": "package a\n"})

	if _, err := take(root, shas[0], "0000000000000000000000000000000000000000"); err == nil {
		t.Fatal("a range that could not be compared returned a clean report")
	}
}
