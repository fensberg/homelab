package repo

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// One commit per action, decided in one file, checked everywhere it is copied.
//
// WHAT WAS WRONG. `actions/checkout` is pinned in 30 steps, `harden-runner` in
// 31, `actions/setup-go` in 12 - 91 pinned references across 14 repositories.
// They all agreed, and nothing required them to. The guard that read them
// (joblaw_test.go) matches on the action NAME and a bare `@`, so it asserts
// harden-runner is first and checkout is present; a lane whose checkout drifted
// to another commit satisfies it completely. zizmor's unpinned-uses covers the
// other half - that a reference is a commit rather than a tag - and is equally
// silent about two commits disagreeing.
//
// WHY A DECLARATION RATHER THAN A COMPARISON. Refusing divergence between 91
// copies still leaves 91 places somebody may edit, and it reports a conflict
// without saying which side is right. One writable home makes the question have
// exactly one answer: the copies become derived, and the guard checks them.
//
// WHY THE COPIES CANNOT SIMPLY GO. GitHub resolves `uses:` before any expression
// context exists, so `${{ }}` is unavailable there - which is why
// .github/actions/versions can centralise scripts/versions.env and cannot do
// this. Wrapping each action in a local composite action would remove the
// literal, and would mean editing thirteen workflows under a protected path for
// every action adopted. The copies stay; their authority does not.
//
// WHAT THIS DELIBERATELY DOES NOT DO. It does not assert that a reference is
// pinned at all - an action on a mutable tag has no 40-character commit and so
// is invisible here. zizmor's unpinned-uses owns that question and this does
// not take a second owner for it.

type actionPinFile struct {
	Actions map[string]struct {
		SHA     string `yaml:"sha"`
		Version string `yaml:"version"`
	} `yaml:"actions"`
}

// `uses: owner/repo[/path]@<40 hex>` with the version comment that follows it.
var pinnedUse = regexp.MustCompile(
	`uses:\s*([A-Za-z0-9._-]+/[A-Za-z0-9._-]+(?:/[^@\s]+)?)@([0-9a-f]{40})[^\S\n]*(?:#[^\S\n]*(\S+))?`)

func loadActionPins(t *testing.T) actionPinFile {
	t.Helper()
	var pins actionPinFile
	body := readRepoFile(t, "tests/action-pins.yml")
	if err := yaml.Unmarshal([]byte(body), &pins); err != nil {
		t.Fatalf("parsing tests/action-pins.yml: %v", err)
	}
	if len(pins.Actions) == 0 {
		t.Fatal("tests/action-pins.yml declares no actions, so every reference in " +
			"the repository would be unchecked")
	}
	return pins
}

type actionUse struct {
	where   string
	ref     string // owner/repo, possibly with a subpath
	repo    string // owner/repo
	sha     string
	version string
}

// everyPinnedUse finds every action reference in the repository.
//
// Enumerated from the tracked files rather than from .github/workflows, so a
// reference in a composite action, a new workflow directory or anywhere else is
// covered because it exists rather than because somebody widened this.
func everyPinnedUse(t *testing.T) []actionUse {
	t.Helper()
	root := repoRoot(t)

	var uses []actionUse
	for _, rel := range trackedMatching(t, func(rel string) bool {
		return authoredHere(rel) &&
			(strings.HasSuffix(rel, ".yml") || strings.HasSuffix(rel, ".yaml"))
	}) {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		for _, m := range pinnedUse.FindAllStringSubmatch(string(body), -1) {
			parts := strings.Split(m[1], "/")
			uses = append(uses, actionUse{
				where:   rel,
				ref:     m[1],
				repo:    parts[0] + "/" + parts[1],
				sha:     m[2],
				version: m[3],
			})
		}
	}
	return uses
}

func TestEveryActionCommitMatchesTheOneDeclaration(t *testing.T) {
	pins := loadActionPins(t)
	uses := everyPinnedUse(t)

	// A parse that stopped matching would find nothing and report success
	// having checked nothing, which is the shape this whole file exists to
	// refuse.
	const fewestPinnedUses = 50
	if len(uses) < fewestPinnedUses {
		t.Fatalf(`only %d pinned action reference(s) were found, and this repository
has many more.

The pattern has stopped matching, so every reference it no longer sees is one
this check silently stopped asking about.`, len(uses))
	}

	used := map[string]bool{}
	for _, u := range uses {
		used[u.repo] = true

		declared, ok := pins.Actions[u.repo]
		if !ok {
			t.Errorf(`%s uses %s, which tests/action-pins.yml does not declare.

Every action's commit is decided in that file and nowhere else. Add a row for
%s with the commit and the version it corresponds to; until then nothing says
which commit this action is supposed to be, and a second reference to it
elsewhere could name a different one with nothing to notice.`,
				u.where, u.ref, u.repo)
			continue
		}

		if u.sha != declared.SHA {
			t.Errorf(`%s pins %s to
  %s
and tests/action-pins.yml declares
  %s

One of these is not the commit anybody chose. If an updater has bumped the
action, update the row in tests/action-pins.yml - that is the whole fix, and it
is the only place this may be written.`, u.where, u.ref, u.sha, declared.SHA)
		}

		// The comment is what a reviewer actually reads. A correct commit under
		// a stale version comment is the quieter failure and the more
		// misleading one.
		if u.version == "" {
			t.Errorf(`%s pins %s with no version comment.

The commit is unreadable on its own, so the comment is what tells a reviewer
which release they are approving. Write it as `+"`# %s`"+`.`,
				u.where, u.ref, declared.Version)
			continue
		}
		if u.version != declared.Version {
			t.Errorf(`%s says %s is %s and tests/action-pins.yml declares %s.

The commit may well be right; the comment beside it is what somebody reviewing
this reads instead of the hash, and it is naming a different release.`,
				u.where, u.ref, u.version, declared.Version)
		}
	}

	// A row nothing uses is a commit nobody is running, kept up to date by an
	// updater for no reason, and a claim that this estate depends on something
	// it does not.
	var declaredNames []string
	for name := range pins.Actions {
		declaredNames = append(declaredNames, name)
	}
	sort.Strings(declaredNames)
	for _, name := range declaredNames {
		if !used[name] {
			t.Errorf(`tests/action-pins.yml declares %s, which nothing uses.

Remove the row. A pin for something that is gone makes the estate's dependency
surface look larger than it is, and the next reference to that action inherits a
commit nobody chose for it.`, name)
		}
	}
}

// Every row is a well-formed decision.
//
// A row with a truncated commit or a missing version is worse than no row: the
// check above compares against it and passes, so the declaration would be
// asserting something nobody can act on.
func TestEveryActionPinRowIsComplete(t *testing.T) {
	pins := loadActionPins(t)
	fullCommit := regexp.MustCompile(`^[0-9a-f]{40}$`)

	for name, row := range pins.Actions {
		if strings.Count(name, "/") != 1 {
			t.Errorf(`the row %q is not an owner/repo pair.

Commits belong to repositories, so a subpath such as codeql-action/init shares
its parent's row rather than having one of its own - otherwise two subpaths of
one repository could be pinned to two different commits.`, name)
		}
		if !fullCommit.MatchString(row.SHA) {
			t.Errorf(`%s is declared as %q, which is not a full 40-character commit.

An abbreviated commit is ambiguous by construction and cannot be compared
against what a workflow actually pins.`, name, row.SHA)
		}
		if strings.TrimSpace(row.Version) == "" {
			t.Errorf(`%s declares no version.

The version is what every reference's comment is checked against, so without it
the comments are unguarded and a reviewer reads whatever was typed.`, name)
		}
	}
}

// The declaration names each action once, and the file says so out loud.
//
// A mapping cannot hold two rows for one key - the second silently wins - so
// this asserts on the TEXT rather than on the parsed result. Without it, a
// duplicated key is a declaration that looks like two decisions and is one.
//
// TestRepositoryHasNoDuplicateKeys reads this file too, and this is deliberately
// the narrower, louder version: it names the action rather than the line, and it
// fails for the reason this particular file cannot tolerate it.
func TestTheActionDeclarationNamesEachActionOnce(t *testing.T) {
	body := readRepoFile(t, "tests/action-pins.yml")

	seen := map[string]int{}
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		// A row key: exactly two spaces of indent, owner/repo, then a colon.
		m := regexp.MustCompile(`^  ([A-Za-z0-9._-]+/[A-Za-z0-9._-]+):\s*$`).
			FindStringSubmatch(line)
		if m != nil {
			seen[m[1]]++
		}
	}

	if len(seen) == 0 {
		t.Fatal("no action rows were found in tests/action-pins.yml, so this " +
			"proves nothing about duplicates")
	}
	for name, n := range seen {
		if n > 1 {
			t.Errorf(`tests/action-pins.yml declares %s %d times.

This file exists so that one action has one commit. A repeated key is not two
decisions - YAML keeps the last and discards the first without a word, so the
row somebody is reading may not be the row in force.`, name, n)
		}
	}
}
