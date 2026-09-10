package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A run is clean when every verb in it is clean, not when the talkative one is.
//
// This is #335. `clerk snag` and `clerk handover` both run on a pull request,
// and only snag was given -pr, so only snag could ever comment - deliberately,
// because two comments repeating one count is noise. The consequence nobody
// looked for: handover filed fourteen alerts, snag found nothing, and the only
// voice in the run said "nothing to raise" on a pull request carrying fifteen
// open findings.
//
// The comment was true about snagging and read as a verdict on the run. So the
// verdict is now computed from every verb's report at once.
func TestAClaimOfNothingToRaiseCoversEveryVerbInTheRun(t *testing.T) {
	quiet, err := sarif(nil, 0)
	if err != nil {
		t.Fatalf("sarif: %v", err)
	}
	loud, err := sarif([]snag{
		{ruleHandover, "scripts/install-dependencies.sh", 64, "downloads Go without saying how"},
	}, 0)
	if err != nil {
		t.Fatalf("sarif: %v", err)
	}

	found, _, err := gather(writeAll(t, quiet, loud))
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("gathering two reports found %d finding(s), want 1", len(found))
	}

	// Every finding sits on a line the diff shows, so the comment stays silent
	// - which is the deliberate behaviour guarded by
	// TestTheCommentCarriesNoCountWhenItSpeaksAtAll. Silence is fine. A false
	// claim of cleanliness is not, and that is all this asserts.
	got := note("reading", found, nil, nil, "")
	if strings.Contains(got, "nothing to raise") {
		t.Errorf("a run holding a finding still claims there is nothing to raise:\n%s\n\n"+
			"This is exactly #335: the verb that found something cannot speak, so the "+
			"verb that found nothing must not speak for it.", got)
	}
}

// The opposite half, so the fix cannot be "never say anything".
//
// Silence would make a clean reading indistinguishable from a clerk that never
// ran, which is the distinction TestTheCommentDistinguishesNothingReviewedFrom
// NothingFound exists to protect. A genuinely clean run still says so.
func TestAGenuinelyCleanRunStillSaysSo(t *testing.T) {
	quiet, err := sarif(nil, 0)
	if err != nil {
		t.Fatalf("sarif: %v", err)
	}

	found, _, err := gather(writeAll(t, quiet, quiet))
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("two empty reports produced %d finding(s)", len(found))
	}
	if got := note("reading", found, nil, nil, ""); !strings.Contains(got, "nothing to raise") {
		t.Errorf("a run where every verb was clean does not say so:\n%s", got)
	}
}

// Reading a report back has to recover what was written, or the run-level
// verdict is computed from something other than what was uploaded.
func TestAReportSurvivesBeingReadBack(t *testing.T) {
	want := []snag{
		{ruleUnsound, "scripts/clerk/llm.go", 42, "nothing reaches this branch"},
		{ruleHandover, "management/cluster/talos.tf", 31, "assumes a nameserver"},
	}
	report, err := sarif(want, 0)
	if err != nil {
		t.Fatalf("sarif: %v", err)
	}

	got, _, err := gather(writeAll(t, report))
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("read back %d finding(s), want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("finding %d came back as %+v, want %+v.\n\n"+
				"A verdict computed from a lossy read is a verdict about something "+
				"other than what was uploaded.", i, got[i], want[i])
		}
	}
}

// A missing report is not an empty one.
//
// The estate's own rule: a check that cannot examine its subject reports that
// as an error rather than as a third, calmer status. An unreadable report
// silently treated as "no findings" is how a run claims to be clean because it
// failed to look.
func TestAnUnreadableReportIsAnErrorRatherThanACleanRun(t *testing.T) {
	if _, _, err := gather([]string{"no-such-report.sarif"}); err == nil {
		t.Error("a report that could not be read was treated as one holding no findings.\n\n" +
			"That turns a failure to look into a clean verdict, which is the one " +
			"thing this surface must never do.")
	}
}

// writeAll puts each report in a scratch directory and returns the paths.
func writeAll(t *testing.T, reports ...[]byte) []string {
	t.Helper()
	dir := t.TempDir()
	var paths []string
	for i, r := range reports {
		p := filepath.Join(dir, fmt.Sprintf("report-%d.sarif", i))
		if err := os.WriteFile(p, r, 0o600); err != nil {
			t.Fatalf("writing %s: %v", p, err)
		}
		paths = append(paths, p)
	}
	return paths
}
