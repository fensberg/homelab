package repo

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Every action's commit is written in exactly one place.
//
// WHAT WAS WRONG. harden-runner was pinned in 31 jobs, checkout in 30, setup-go
// in 12 - 91 references across 14 repositories. They agreed, and nothing made
// them: a guard matched the action's NAME, and zizmor asks only whether a
// reference is a commit rather than a tag. One lane drifting to a different
// commit of checkout satisfied both.
//
// WHY THIS REFUSES A SECOND COPY RATHER THAN A DISAGREEING ONE. A rule that
// copies must agree still leaves every copy as a place to edit, and reports a
// conflict without saying which side is right. The first version of this guard
// did exactly that - it required 91 copies to match a declaration file, which
// was a 92nd place the commit was written - and was reverted. So a second
// `uses:` line naming an action that already has one is the failure, whatever
// commit it names.
//
// HOW IT IS SATISFIABLE AT ALL. `uses:` is resolved before any expression
// context exists, so a commit there cannot be a variable. What is possible is a
// composite action under .github/workflows/ holding the one literal, referenced
// everywhere else with `$/`, which resolves at the running commit and needs no
// checkout. .github/workflows/mobilize, checkout, setup-go, upload-sarif,
// github-script and app-token are those.
//
// PER ACTION PATH, AND ONE COMMIT PER REPOSITORY. github/codeql-action/init and
// github/codeql-action/analyze are different paths and each may appear once. But
// a commit belongs to a repository, so every path of one repository must name
// the same one - otherwise consolidating by path would permit two commits of one
// action at once.
//
// JUDGED AS IT WILL BE. Workflows reach the repository through a hand-over
// patch, so while one is outstanding this reads the tree with it applied - the
// same view the mutation ledger proves guards against. Read the tracked copy
// instead and this is red for the whole hand-over window about a state that is
// already on its way out.
//
// WHAT THIS DOES NOT CHECK. Whether a reference is pinned at all. A `uses:` on a
// tag has no 40-character commit and is invisible here; zizmor's unpinned-uses
// owns that question and fails the Workflow Scan lane on it.

var (
	usesLine     = regexp.MustCompile(`(?m)^\s*(?:-\s+)?uses:\s*["']?([^\s"'#]+)`)
	pinnedAction = regexp.MustCompile(`^([A-Za-z0-9._-]+/[A-Za-z0-9._-]+)((?:/[^@]+)?)@([0-9a-f]{40})$`)
)

// intendedRoot is the repository as it will be once outstanding patches land.
func intendedRoot(t *testing.T) string {
	t.Helper()
	if len(outstandingPatches(t)) == 0 {
		return repoRoot(t)
	}
	return scratchRepo(t)
}

// runnable reports whether GitHub executes a file's `uses:` lines: a workflow
// at the top of .github/workflows, or the metadata of any composite action.
func runnable(rel string) bool {
	base := filepath.Base(rel)
	if base == "action.yml" || base == "action.yaml" {
		return true
	}
	dir := filepath.ToSlash(filepath.Dir(rel))
	return dir == ".github/workflows" &&
		(strings.HasSuffix(base, ".yml") || strings.HasSuffix(base, ".yaml"))
}

func TestEveryActionCommitIsWrittenOnce(t *testing.T) {
	root := intendedRoot(t)

	type site struct{ file, commit string }
	byPath := map[string][]site{}              // owner/repo[/path] -> every place it is written
	byRepo := map[string]map[string][]string{} // owner/repo -> commit -> paths naming it
	selfRefs := 0
	files := 0

	for _, rel := range trackedFilesIn(t, root) {
		if !authoredHere(rel) || !runnable(rel) {
			continue
		}
		files++
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		for _, m := range usesLine.FindAllStringSubmatch(string(body), -1) {
			ref := m[1]
			if strings.HasPrefix(ref, "$/") {
				selfRefs++
				continue
			}
			pm := pinnedAction.FindStringSubmatch(ref)
			if pm == nil {
				continue // ./, docker://, or unpinned - zizmor owns the last
			}
			repo, path, commit := pm[1], pm[1]+pm[2], pm[3]
			byPath[path] = append(byPath[path], site{rel, commit})
			if byRepo[repo] == nil {
				byRepo[repo] = map[string][]string{}
			}
			byRepo[repo][commit] = append(byRepo[repo][commit], path)
		}
	}

	var paths []string
	for p := range byPath {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		sites := byPath[p]
		if len(sites) < 2 {
			continue
		}
		var where []string
		for _, s := range sites {
			where = append(where, "  "+s.file+"  @"+s.commit[:12])
		}
		t.Errorf(`%s is written in %d places:

%s

An action's commit may be written once. A second copy is a second place to
update, and it can name a different commit with nothing to notice. Reference the
shared composite action with $/ instead - or, if this action has none yet, add
one under .github/workflows/<name>/action.yml holding the only literal, the way
setup-go and upload-sarif are.`, p, len(sites), strings.Join(where, "\n"))
	}

	var repos []string
	for r := range byRepo {
		repos = append(repos, r)
	}
	sort.Strings(repos)
	for _, r := range repos {
		if len(byRepo[r]) < 2 {
			continue
		}
		var detail []string
		for commit, ps := range byRepo[r] {
			sort.Strings(ps)
			detail = append(detail, "  "+commit+"  "+strings.Join(ps, ", "))
		}
		sort.Strings(detail)
		t.Errorf(`%s is pinned to %d different commits across its action paths:

%s

A commit belongs to the repository, not to a path inside it. Two commits of one
action running side by side is the drift writing each path once was meant to
end.`, r, len(byRepo[r]), strings.Join(detail, "\n"))
	}

	// A walk that found nothing would pass every rule above. These floors are
	// the smallest numbers that could describe this repository at all.
	const fewestRunnableFiles, fewestPinnedPaths = 10, 10
	if files < fewestRunnableFiles {
		t.Fatalf("only %d workflow or action file(s) were read, so this checked almost nothing", files)
	}
	if len(byPath) < fewestPinnedPaths {
		t.Fatalf("only %d pinned action path(s) were found; the pattern has stopped matching", len(byPath))
	}
	if selfRefs == 0 {
		t.Fatal(`no $/ reference was found anywhere. Every job starts with
$/.github/workflows/mobilize, so finding none means the read is wrong - or the
shared actions are gone and every copy is back.`)
	}
}
