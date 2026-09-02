package main

import (
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
