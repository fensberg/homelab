package phases

import (
	"errors"
	"strings"
	"testing"
)

// The progress line printed while a check is still failing used to be the
// error's first line, which for the Flux check was "4 Flux resource(s) not
// reconciled:" - a colon promising a list that had just been truncated away.
// And because the count rises as Flux discovers more to reconcile, a run
// progressing normally read as one going backwards.
func TestSummariseWait_NamesWhatItIsWaitingFor(t *testing.T) {
	err := errors.New("4 Flux resource(s) not reconciled:\n  helmrelease/self-hosted\n  kustomization/infra-configs")
	got := summariseWait(err)

	for _, want := range []string{"4 Flux resource(s) not reconciled", "helmrelease/self-hosted", "kustomization/infra-configs"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary omits %q; got %q", want, got)
		}
	}
	if strings.Contains(got, ":\n") || strings.HasSuffix(got, ":") {
		t.Errorf("summary ends on a colon promising a list that is not there: %q", got)
	}
}

// The database check's message is one line followed by a blank line and
// several paragraphs of explanation. Those belong in the final failure, not in
// a line printed every fifteen seconds.
func TestSummariseWait_StopsAtTheExplanation(t *testing.T) {
	err := errors.New("the state database has 0 of 3 instances ready.\n\nThis is the exact failure the first ignition shipped over. A degraded\nCloudNativePG cluster still answers on its port.")
	got := summariseWait(err)

	if got != "the state database has 0 of 3 instances ready." {
		t.Errorf("expected just the headline, got %q", got)
	}
}

func TestSummariseWait_SingleLineIsUnchanged(t *testing.T) {
	if got := summariseWait(errors.New("no Kustomizations or HelmReleases exist yet")); got != "no Kustomizations or HelmReleases exist yet" {
		t.Errorf("got %q", got)
	}
}

// A cluster with many outstanding resources should not print a paragraph every
// fifteen seconds.
func TestSummariseWait_Truncates(t *testing.T) {
	var b strings.Builder
	b.WriteString("30 Flux resource(s) not reconciled:")
	for i := 0; i < 30; i++ {
		b.WriteString("\n  helmrelease/some-fairly-long-release-name")
	}
	got := summariseWait(errors.New(b.String()))
	if len(got) > 160 {
		t.Errorf("summary is %d chars; a progress line should stay on one line: %q", len(got), got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("a truncated summary should say so, got %q", got)
	}
}
