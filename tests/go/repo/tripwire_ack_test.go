package repo

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The sensitive-path tripwire's acknowledgement step, guarded against two
// defects that let it report an acknowledgement nobody gave. Both were found
// on a live pull request and are recorded in #108.
//
// A third defect is deliberately not covered here, because it cannot be: a
// label survives new commits, since the workflow re-runs on `synchronize` and
// GitHub dismisses stale reviews on a push but never removes labels. Label a
// pull request, push anything, and the gate is satisfied for content nobody
// looked at. That is the mechanism working as designed and is why the
// acknowledgement is moving to an attestation bound to the content reviewed -
// see docs/sensitive-path-tripwire.md. No assertion on this file can fix it.
func acknowledgementStep(t *testing.T) string {
	t.Helper()
	path := filepath.Join(repoRoot(t), ".github", "workflows", "sensitive-paths.yml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the tripwire workflow: %v", err)
	}
	body := string(b)

	const marker = "Require an explicit acknowledgement"
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("the tripwire has no %q step. That step is the entire gate: "+
			"without it a change to a sensitive path merges with no human "+
			"acknowledgement at all.", marker)
	}
	return body[i:]
}

// The timeline pages at thirty events, so on a long pull request the labelling
// event is not on the first page and an unpaginated query silently finds
// nobody. Measured on #105: the same query returned zero labelling events
// without --paginate and two with it, and the check refused a pull request
// that was correctly labelled.
//
// It fails closed, so the damage is a blocked merge rather than an admitted
// one - but the workaround is an admin override, which is the gate being
// stepped around rather than satisfied, and it bites precisely the long-lived
// pull requests most likely to touch something sensitive.
func TestTheAcknowledgementQueryPaginatesTheTimeline(t *testing.T) {
	step := acknowledgementStep(t)

	timeline := regexp.MustCompile(`gh api[^\n]*\bissues/\$\{?PR\}?/timeline`)
	loc := timeline.FindStringIndex(step)
	if loc == nil {
		return // no timeline query at all; nothing to paginate
	}

	// Look at the invocation itself rather than the whole step, so an unrelated
	// --paginate elsewhere cannot satisfy this.
	line := step[loc[0]:]
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	if !strings.Contains(line, "--paginate") {
		t.Errorf("the timeline query does not use --paginate:\n    %s\n\n"+
			"gh returns the first page only, so on a pull request with more than "+
			"about thirty timeline events the labelling event is invisible and the "+
			"check reports that nobody applied the label. See #108.", strings.TrimSpace(line))
	}
}

// The acknowledgement has to reflect the pull request as it is now, not a
// moment in its history. Reading only `labeled` events and taking the last one
// means a label that was applied and then removed still satisfies the check -
// which fails open, and is trivial to do deliberately.
//
// The fix is to consult the current label set. This asserts that something in
// the step does so, rather than asserting one particular spelling, because
// several are reasonable - `gh pr view --json labels`, or the event payload's
// own label list.
func TestTheAcknowledgementChecksTheCurrentLabelNotOnlyHistory(t *testing.T) {
	step := acknowledgementStep(t)

	// Any of these reads the label set as it stands rather than the timeline.
	current := []string{
		"--json labels",
		"pull_request.labels",
		"github.event.pull_request.labels",
	}
	for _, c := range current {
		if strings.Contains(step, c) {
			return
		}
	}

	if !strings.Contains(step, "timeline") {
		return // not timeline-based at all; this defect does not apply
	}

	t.Errorf("the acknowledgement is decided from the timeline alone.\n\n" +
		"It selects `labeled` events and ignores `unlabeled`, so a label that was " +
		"applied and then removed still satisfies it - the check reflects a moment " +
		"in the pull request's history rather than its current state. Read the " +
		"current label set as well as the timeline: the timeline says who " +
		"acknowledged, the label set says whether the acknowledgement still " +
		"stands. See #108.")
}

// A bot must not be able to acknowledge on the human's behalf. This is the
// property the whole gate rests on, and it was already correct - asserted here
// so a rewrite of the step cannot quietly drop it.
func TestABotCannotAcknowledge(t *testing.T) {
	step := acknowledgementStep(t)
	if !strings.Contains(step, "[bot]") {
		t.Error("the acknowledgement step no longer rejects an actor whose name ends " +
			"in [bot].\n\nThe gate exists so a human looks at a dangerous change; an " +
			"agent that can label its own pull request satisfies the check without " +
			"anybody having looked at anything.")
	}
}
