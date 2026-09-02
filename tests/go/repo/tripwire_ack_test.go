package repo

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The sensitive-path acknowledgement, and the law that it happens at all.
//
// It used to be a label, and the label outlived the diff it acknowledged.
// GitHub dismisses stale reviews when a branch moves and never touches labels,
// so `sensitive-reviewed` survived every later commit: touch a sensitive path,
// read the comment, apply the label, push a fixup, merge. The gate was
// satisfied for a diff nobody had looked at, and a fixup is exactly where a
// deleted assertion hides. Recorded as the third defect in #108.
//
// It is a review conversation now, keyed to a digest of the sensitive part of
// the diff. Change one of those files and the marker changes, no thread
// matches it, and a new conversation opens - so an acknowledgement cannot
// outlive what it acknowledged, by construction rather than by a rule bolted
// on afterwards.
//
// These tests are the half a unit test cannot see. scripts/attestation has a
// test for every branch of the decision; nothing there says the workflow calls
// it, or that the thing it replaced is really gone.

// The behaviour could have lived as JavaScript inside the workflow, where
// nothing lints it, nothing runs it against known input, and it exists only
// because somebody remembered to write it. That is a convention, and a
// convention fails.
func TestTheTripwireOpensAConversation(t *testing.T) {
	body := readSensitivePathsWorkflow(t)

	// The invocation, not a mention of it. An earlier version of this matched
	// the string anywhere in the file, so the comment above the step satisfied
	// it and deleting the step itself passed - the test describing behaviour
	// that had been removed.
	if !strings.Contains(body, "go run ./scripts/attestation") {
		t.Fatal("the workflow does not run scripts/attestation.\n\n" +
			"Then nothing opens the review conversation a sensitive change has to be " +
			"acknowledged in, and the gate is a comment nobody has to answer.")
	}
	if !strings.Contains(body, "-digest") {
		t.Error("the attestation is not keyed to a digest of the sensitive diff, so an " +
			"acknowledgement would outlive the change it acknowledged - which is the " +
			"exact defect the label had")
	}
	// Deliberately NOT a trigger on conversation resolution.
	//
	// `pull_request_review_thread` is a webhook event that was never ported to
	// workflow triggers - actionlint refuses it, and it was in this workflow
	// until actionlint said so. A check that blocked on an open conversation
	// could then never be turned green, because nothing would re-run it.
	// Matched as a trigger key, not as a string. Written the loose way first,
	// it was satisfied by the comment above the triggers explaining why the
	// event is absent - the third time this week a check has matched prose
	// describing behaviour rather than the behaviour.
	if reviewThreadTrigger.MatchString(body) {
		t.Error("the workflow triggers on pull_request_review_thread, which is not a " +
			"workflow trigger. It is a webhook event only, so this workflow would " +
			"never run - and anything relying on it to re-run after a resolution is a " +
			"deadlock.")
	}
}

// The digest must cover the sensitive files and not the whole diff. Digesting
// everything means every push invalidates the acknowledgement, which trains
// people to resolve without reading - the same end state as a label they click
// without looking, arrived at from the opposite direction.
func TestTheDigestCoversOnlyTheSensitiveFiles(t *testing.T) {
	body := readSensitivePathsWorkflow(t)
	i := strings.Index(body, "Digest the sensitive part of the diff")
	if i < 0 {
		t.Fatal("nothing digests the sensitive part of the diff, so the acknowledgement " +
			"is bound to a moment rather than to content")
	}
	step := body[i:min(i+900, len(body))]
	if !strings.Contains(step, "outputs.files") {
		t.Error("the digest does not read the sensor's file list, so it is not scoped to " +
			"the sensitive files")
	}
}

// Two mechanisms for one acknowledgement is two things to keep in step, and
// the weaker one becomes the one people use.
func TestTheLabelMechanismIsGone(t *testing.T) {
	body := readSensitivePathsWorkflow(t)
	for _, dead := range []string{"removeLabel", "addLabels", "--json labels", "labeled"} {
		if strings.Contains(body, dead) {
			t.Errorf("the workflow still manipulates or reads labels (%q). The "+
				"acknowledgement is a conversation now; a label alongside it is a "+
				"second, weaker gate that will drift from this one.", dead)
		}
	}
}

// The gate has to be able to refuse. A workflow that opens a conversation and
// exits zero is decoration: GitHub's own conversation-resolution rule would
// still block the merge, but nothing would check *who* resolved it, which is
// the one thing this exists to check.
func TestTheTripwireCanRefuse(t *testing.T) {
	body := readSensitivePathsWorkflow(t)
	if strings.Contains(body, "continue-on-error: true") {
		t.Error("a step is allowed to fail without failing the job, so the gate cannot refuse")
	}
	if !strings.Contains(body, "steps.sensor.outputs.tripped == 'true'") {
		t.Error("the acknowledgement step is not conditioned on the tripwire firing, so it " +
			"either runs on every pull request or on none")
	}
}

func readSensitivePathsWorkflow(t *testing.T) string {
	t.Helper()
	path := filepath.Join(repoRoot(t), ".github", "workflows", "sensitive-paths.yml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(body)
}

// `pull_request_review_thread:` as a workflow trigger - at the indentation
// `on:` uses for an event, rather than anywhere in the file.
var reviewThreadTrigger = regexp.MustCompile(`(?m)^\s{2}pull_request_review_thread:`)
