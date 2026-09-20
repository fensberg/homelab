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
// time, and should never cut a name in half: a truncated name is one nobody
// can search for.
func TestSummariseWait_ShortensByItemRatherThanByCharacter(t *testing.T) {
	var b strings.Builder
	b.WriteString("30 Flux resource(s) not reconciled:")
	for i := 0; i < 30; i++ {
		b.WriteString("\n  helmrelease/some-fairly-long-release-name")
	}
	got := summariseWait(errors.New(b.String()))

	if len(got) > 200 {
		t.Errorf("summary is %d chars; a progress line should stay readable: %q", len(got), got)
	}
	if strings.Contains(got, "...") {
		t.Errorf("a name was cut mid-word rather than dropped whole: %q", got)
	}
	if !strings.Contains(got, "+27 more") {
		t.Errorf("the summary should say how many it left out, got %q", got)
	}
}

// Flux lists a Kustomization waiting on an unready dependency beside the thing
// that is actually failing, and the waiters are usually the majority - so the
// one item worth reading was the one being dropped. This is the shape the
// operator was shown for an hour: five resources, and the only one named was
// a consequence of the other four (#458).
func TestSummariseWait_NamesTheCauseRatherThanTheWaiters(t *testing.T) {
	err := errors.New("5 Flux resource(s) not reconciled:" +
		"\n  Kustomization flux-system/infra-configs: dependency 'flux-system/infra-controllers' is not ready" +
		"\n  Kustomization flux-system/workloads-production: dependency 'flux-system/infra-configs' is not ready" +
		"\n  Kustomization flux-system/workloads-staging: dependency 'flux-system/infra-configs' is not ready" +
		"\n  HelmRelease monitoring/kube-prometheus-stack: install retries exhausted")

	got := summariseWait(err)

	if !strings.Contains(got, "kube-prometheus-stack") {
		t.Errorf("the summary drops the only item that is actually failing: %q", got)
	}
	if strings.Contains(got, "infra-configs: dependency") {
		t.Errorf("the summary leads with a consequence rather than the cause: %q", got)
	}
	if !strings.Contains(got, "3 waiting on them") {
		t.Errorf("the summary should still account for the waiters, got %q", got)
	}
}

// Everything waiting on something else is still worth printing: with no cause
// in the list, the waiters are all there is to say.
func TestSummariseWait_KeepsWaitersWhenThatIsAllThereIs(t *testing.T) {
	err := errors.New("2 Flux resource(s) not reconciled:" +
		"\n  Kustomization flux-system/a: dependency 'flux-system/b' is not ready" +
		"\n  Kustomization flux-system/c: dependency 'flux-system/b' is not ready")

	if got := summariseWait(err); !strings.Contains(got, "flux-system/a") {
		t.Errorf("with only waiters in the list, they are the summary; got %q", got)
	}
}
