package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var approved = []string{
	"https://github.com/pre-commit/pre-commit-hooks",
	"https://github.com/rhysd/actionlint",
}

// Default deny. Anything not on the list is refused, rather than anything on a
// denylist being refused - the difference being what happens to a supplier
// nobody thought about.
func TestAnUndeclaredHookIsRefused(t *testing.T) {
	f := Check([]string{"https://github.com/somebody/handy-looking-hook"}, nil, approved)
	if len(f) != 1 {
		t.Fatalf("an undeclared hook was allowed: %+v", f)
	}
	if f[0].Where != "the hook configuration" {
		t.Errorf("the finding does not say where it was found: %+v", f[0])
	}
}

// The question nobody could answer by inspection. pre-commit clones into
// generated directory names, so an eighth repository in the cache looks
// exactly like the seven that belong.
func TestAnUndeclaredRepositoryInTheCacheIsRefused(t *testing.T) {
	f := Check(nil, []string{"https://github.com/somebody/left-behind"}, approved)
	if len(f) != 1 {
		t.Fatalf("an undeclared cached repository was allowed: %+v", f)
	}
	if f[0].Where != "the local cache" {
		t.Errorf("the finding does not distinguish the cache from the config: %+v", f[0])
	}
}

func TestApprovedRepositoriesPass(t *testing.T) {
	if f := Check(approved, approved, approved); len(f) != 0 {
		t.Fatalf("an approved supplier was refused: %+v", f)
	}
}

// Punctuation must not decide it. A guard that refuses a commit because a
// remote carries .git is a guard somebody disables, and then nothing is
// checked at all.
func TestSpellingsOfOneRepositoryCompareEqual(t *testing.T) {
	for _, spelling := range []string{
		"https://github.com/rhysd/actionlint.git",
		"https://github.com/rhysd/actionlint/",
		"git@github.com:rhysd/actionlint.git",
		"https://github.com/RHYSD/Actionlint",
	} {
		if f := Check(nil, []string{spelling}, approved); len(f) != 0 {
			t.Errorf("%s was refused despite being the same repository as an approved one", spelling)
		}
	}
}

// Declared and unused is not a reason to refuse a commit. It is a tidiness
// problem, the test suite already reports it, and a guard that blocks work
// over an unused list entry gets switched off.
func TestAnUnusedDeclarationDoesNotBlockACommit(t *testing.T) {
	if f := Check(nil, nil, approved); len(f) != 0 {
		t.Fatalf("approved suppliers that nothing uses blocked a commit: %+v", f)
	}
}

// Same repository in both places is one problem, not two.
func TestOneRepositoryIsReportedOncePerPlace(t *testing.T) {
	bad := []string{"https://github.com/somebody/x"}
	f := Check(bad, bad, approved)
	if len(f) != 2 {
		t.Fatalf("expected one finding for the config and one for the cache, got %+v", f)
	}
}

func TestTheRefusalSaysWhatToDo(t *testing.T) {
	msg := Explain(Check([]string{"https://github.com/somebody/x"}, nil, approved))
	for _, want := range []string{"approved-suppliers.yml", "remove the hook", "pre-commit gc"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not mention %q, so it names a problem and no remedy", want)
		}
	}
}

// With no cache at all - the runner's situation, before anything has been
// downloaded - the configured half still has to be judged.
//
// This is the case -before-install exists for. On a workstation the cache is
// the half nothing else can see, and an unreadable one fails closed; on a
// runner there is deliberately no cache yet, because the whole point is to
// refuse before one can exist. Checking nothing would be the natural way to
// get that wrong, and it would look exactly like approval.
func TestAnUnapprovedSupplierIsFoundWithNoCacheAtAll(t *testing.T) {
	findings := Check(
		[]string{"https://github.com/example/approved", "https://github.com/example/not-approved"},
		nil,
		[]string{"https://github.com/example/approved"},
	)
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(findings), findings)
	}
	if findings[0].Where != "the hook configuration" {
		t.Errorf("found in %q, want the hook configuration", findings[0].Where)
	}
}

// The gate has to work wherever it is invoked from.
//
// It ran from the repository root as a git hook, so relative paths worked and
// nothing recorded that the working directory was an input. CI runs it as
// `go run -C scripts/gatehouse .` - each program is its own module, so there
// is nothing at the root to resolve a package path against - which puts the
// working directory inside the module. The gate failed with "no such file or
// directory" instead of a verdict, on the pull request that introduced it.
//
// The test that was meant to keep that step wired asserted it existed and came
// before anything that downloads. Both were true. Neither says the command can
// run, which is the third time that distinction has cost something here.
func TestTheGateFindsTheRepositoryFromAnyDirectory(t *testing.T) {
	root := t.TempDir()
	// A real checkout marker. In a worktree `.git` is a file rather than a
	// directory, which is why the check stats it instead of listing it.
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "scripts", "gatehouse", "internal")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, dir := range []string{root, deep} {
		t.Run(dir, func(t *testing.T) {
			t.Chdir(dir)
			got, err := repositoryRoot()
			if err != nil {
				t.Fatalf("running from %s: %v", dir, err)
			}
			// macOS resolves the temp directory through a symlink, so compare
			// what the filesystem says rather than the strings.
			want, err := filepath.EvalSymlinks(root)
			if err != nil {
				t.Fatal(err)
			}
			gotResolved, err := filepath.EvalSymlinks(got)
			if err != nil {
				t.Fatal(err)
			}
			if gotResolved != want {
				t.Errorf("found %s, want %s", gotResolved, want)
			}
		})
	}
}

// Outside a checkout it has to say so rather than fall back to something.
// Reporting "no unapproved supplier found" because it could not find the
// configuration is the failure mode this whole guard exists to avoid.
func TestTheGateRefusesOutsideACheckout(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := repositoryRoot(); err == nil {
		t.Fatal("running outside a git checkout returned a root, so the gate would " +
			"read some other directory's configuration or silently check nothing")
	}
}
