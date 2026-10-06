package repo

import (
	"regexp"
	"testing"
)

// No workflow reaches one of this repository's own actions by a relative path.
//
// `uses: ./.github/...` resolves against whatever the workspace holds when the
// step runs: it needs a checkout to have happened, and it reads the checkout's
// copy of the action and not the one at the commit being run. `$/` is GitHub's
// reference to the repository itself and has neither property, which is why
// zizmor's self-repository audit asks for it.
//
// Twelve workflows were excused from that audit for as long as the two
// workflow linters disagreed about the syntax, and two guards here kept the
// list of excuses honest. The disagreement ended, the workflows moved, and the
// list is empty; this is what stops it being needed again. zizmor owns the
// finding. This says the same thing in seconds on a machine without zizmor,
// and names the file.
func TestNoWorkflowReachesALocalActionByARelativePath(t *testing.T) {
	relative := regexp.MustCompile(`(?m)^\s*(?:- )?uses:\s*["']?\./`)
	checked := 0
	for _, rel := range trackedFiles(t) {
		if !runnable(rel) {
			continue
		}
		checked++
		if relative.MatchString(stripYAMLComments(readRepoFile(t, rel))) {
			t.Errorf("%s reaches a local action with a relative `uses: ./...`.\n\n"+
				"Write it as `uses: $/.github/...`: it resolves at the commit being run, with no "+
				"checkout, and cannot be redirected by whatever a workspace holds.", rel)
		}
	}
	if checked == 0 {
		t.Fatal("found no workflows or actions, so nothing was checked")
	}
}
