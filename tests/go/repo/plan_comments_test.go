package repo

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Every comment this workflow posts replaces itself rather than appending.
//
// A surface that accumulates is one that teaches a reader to stop looking at
// it, which costs far more than the thing being reported.

// There is no "a plan lane declares no environment" check here, and now
// there does not need to be: plans run on GitHub's runner against the
// as-built record and name no environment at all (#554), and
// TestNoPullRequestJobRunsOnTheEstatesRunner holds the reason that is safe.
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
	scripts := sharedScripts(t)

	total := 0
	var names []string
	for name := range scripts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		body := scripts[name]
		creates := regexp.MustCompile(`issues\.createComment`).FindAllStringIndex(body, -1)
		total += len(creates)
		for _, loc := range creates {
			line := 1 + strings.Count(body[:loc[0]], "\n")
			// Everything before the post, in the same script. A marker and a
			// lookup have to appear before it.
			before := body[:loc[1]]

			// The PREDICATE, not the word "marker". Looking for that word
			// matches the prose explaining the mechanism as readily as the
			// mechanism, which is the change-detector shape this repository
			// refuses by name - and the mutation ledger caught this check in
			// exactly that state.
			if !strings.Contains(before, "listComments") {
				t.Errorf(`%s.js:%d posts a comment without looking for an earlier one.

There is then nothing to find it by on the next push, so every run adds
another. That is noise of the specific kind that makes a reviewer stop reading
the comments - on the lane that comments about changes to real infrastructure.`, name, line)
				continue
			}
			if !strings.Contains(before, "body.startsWith(") {
				t.Errorf(`%s.js:%d lists the comments and matches none of them against a
marker.

Reading the comments and then posting regardless is the same appending
behaviour with an extra API call in front of it.`, name, line)
			}
			if !strings.Contains(before, "deleteComment") {
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
