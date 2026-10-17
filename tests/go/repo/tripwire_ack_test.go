package repo

import (
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
// That the workflow calls it, keyed to a digest of the sensitive files and
// able to refuse, is a configuration rule now
// (policy/conftest/sensitive_paths.rego). What is left here is that the
// thing it replaced is really gone.

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

func readSensitivePathsWorkflow(t *testing.T) string {
	t.Helper()
	return workflowText(t, "sensitive-paths.yml")
}
