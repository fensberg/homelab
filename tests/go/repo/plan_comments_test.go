package repo

import (
	"slices"
	"sort"
	"testing"
)

// Every comment this workflow posts replaces itself rather than appending.
//
// A surface that accumulates is one that teaches a reader to stop looking at
// it, which costs far more than the thing being reported.

// There is no "a plan lane declares no environment" check here, and now
// there does not need to be: plans run on GitHub's runner against the
// as-built record and name no environment at all (#554), and
// policy/conftest/pull_request_runners.rego holds the reason that is safe.
// #158 - deployment records accumulating on a lane that never deploys -
// closes with it.

// Every comment the workflow posts is found and replaced, not appended.
//
// Two of the three paths already did this. The workload plan's did not: it
// called createComment unconditionally, so every `synchronize` added another
// and the oldest sat at the top (#160). Nobody had seen it because the job
// gates on `environments/` and `modules/`, which did not exist when it was
// written - so it would have started spamming on the first pull request of
// epoch 02, which is exactly when nobody would remember it was known.
//
// Asserted as a class rather than as "the workload plan has a marker", because
// the failure is not that one path was missed. It is that a new comment path
// looks complete without one, and the next one added would go the same way.
func TestEveryCommentPathReplacesItselfRatherThanAppending(t *testing.T) {
	// Every script the workflows run. They were inline `script:` blocks in
	// deploy-infrastructure.yml, and are files beside
	// $/.github/workflows/github-script now - so this reads the files, and
	// would read a new one the moment it existed.
	heavy(t, "semgrep reads the shared scripts for their comment calls, a few seconds")
	scripts := sharedScripts(t)

	// Where each call is, script by script, found by Semgrep.
	calls := map[string]any{
		"id": "comment-calls", "languages": []string{"generic"}, "severity": "INFO",
		"message": "$CALL", "metadata": map[string]any{"about": "calls"},
		"pattern-regex": `(?P<CALL>issues\.createComment|listComments|body\.startsWith\(|deleteComment)`,
	}
	var files []string
	nameOf := map[string]string{}
	for name, rel := range scripts {
		files = append(files, rel)
		nameOf[rel] = name
	}
	sort.Strings(files)
	at := map[string]map[string][]int{} // script -> call -> lines
	for _, f := range scanText(t, repository(t), []map[string]any{calls}, files) {
		name := nameOf[f.Path]
		if at[name] == nil {
			at[name] = map[string][]int{}
		}
		at[name][f.Text] = append(at[name][f.Text], f.Line)
	}

	total := 0
	var names []string
	for name := range scripts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		creates := at[name]["issues.createComment"]
		sort.Ints(creates)
		total += len(creates)
		for _, line := range creates {
			// Everything up to the post, in the same script. A marker and a
			// lookup have to appear before it.
			before := func(call string) bool {
				return slices.ContainsFunc(at[name][call], func(l int) bool { return l <= line })
			}
			if !before("listComments") {
				t.Errorf(`%s.js:%d posts a comment without looking for an earlier one.

There is then nothing to find it by on the next push, so every run adds
another. That is noise of the specific kind that makes a reviewer stop reading
the comments - on the lane that comments about changes to real infrastructure.`, name, line)
				continue
			}
			if !before("body.startsWith(") {
				t.Errorf(`%s.js:%d lists the comments and matches none of them against a
marker.

Reading the comments and then posting regardless is the same appending
behaviour with an extra API call in front of it.`, name, line)
			}
			if !before("deleteComment") {
				t.Errorf(`%s.js:%d finds the previous comment and does not remove it.

Editing in place is the other half of this and was rejected for a stated
reason (#246): an edited comment keeps the position it was first posted at
while its contents change underneath, so it ends up sitting beside a push that
no longer produced it.`, name, line)
			}
		}
	}

	// Two: the estate's plan and its absence. The workload tier's plan went
	// with its job (#554).
	if total < 2 {
		t.Fatalf(`found %d createComment call(s) across the shared scripts, and there are
two plan comments.

The read has stopped matching, so a path that appends could be added without
this noticing.`, total)
	}
}
