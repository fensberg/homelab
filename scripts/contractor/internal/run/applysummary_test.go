package run

import (
	"strings"
	"testing"
)

// The guard that did not exist.
//
// Every leak check in this repository scans files at rest: tests/go/repo looks
// for name-shaped strings in the tree, and tests/go/integration reads the real
// names from the vault and looks for them in the tree. None of them could ever
// have caught this, because the leaked value was never in a file. The source
// says `net.Name`; the site's real name only exists at run time, in the
// program's own output, and a scan of the repository cannot see that.
//
// So this tests the output path instead. The recorded stream below is the shape
// tofu actually emits, including an attribute value in a planned change - which
// is exactly how the site name reached a public Actions log.
const recordedApplyStream = `{"@level":"info","@message":"Plan: 1 to add","type":"planned_change","change":{"resource":{"addr":"tailscale_tailnet_key.overlay"},"action":"create"}}
{"@level":"info","@message":"tailscale_tailnet_key.overlay: Creating...","type":"apply_start","hook":{"resource":{"addr":"tailscale_tailnet_key.overlay"},"action":"create"}}
{"@level":"info","@message":"  + description = \"NORTHVALE subnet router\"","type":"planned_change","change":{"resource":{"addr":"tailscale_tailnet_key.overlay"}}}
{"@level":"info","@message":"tailscale_tailnet_key.overlay: Creation complete","type":"apply_complete","hook":{"resource":{"addr":"tailscale_tailnet_key.overlay"},"action":"create"}}
{"@level":"info","@message":"Apply complete!","type":"change_summary","changes":{"add":1,"change":0,"remove":0}}
`

// NORTHVALE stands in for a real site name. It is deliberately not this
// estate's - a test that had to hold the real one would be the leak it is
// checking for, which is the same reason the hermetic name check cannot hold
// names either.
const standInSiteName = "NORTHVALE"

func TestApplySummaryNeverEmitsAnAttributeValue(t *testing.T) {
	lines, failed := summariseApply(strings.NewReader(recordedApplyStream))

	joined := strings.Join(append(append([]string{}, lines...), failed...), "\n")
	if strings.Contains(joined, standInSiteName) {
		t.Errorf(`the apply summary emitted an attribute value.

Got:
%s

A converge runs in a public repository's Actions log, so anything printed here
is published. Report the address and the verb; never the value.`, joined)
	}

	// And it must still be useful, or the safe version is one nobody keeps.
	if len(lines) == 0 {
		t.Fatal("the summary emitted nothing at all, which is not a safe version of the output - it is no output")
	}
	if !strings.Contains(joined, "tailscale_tailnet_key.overlay") {
		t.Error("the summary does not name the resource that changed, so it cannot tell an operator what happened")
	}
	if !strings.Contains(joined, "1 added, 0 changed, 0 destroyed") {
		t.Error("the summary drops the change counts, which are the one number a converge is judged by")
	}
}

// A diagnostic's detail can quote the value that caused the error, so it is
// withheld where the output is published - and only there. Suppressing it on a
// workstation costs the person debugging the one thing that would help and buys
// nothing, which is what happened the first time this fired: "Failed to create
// key", reason stripped, on a terminal only the operator could see.
func TestApplySummaryWithholdsDiagnosticDetailOnlyInPublicLogs(t *testing.T) {
	stream := `{"@level":"error","type":"diagnostic","diagnostic":{"severity":"error","summary":"Failed to create key","detail":"the OAuth client is not permitted to mint NORTHVALE keys"}}
`
	t.Setenv("GITHUB_ACTIONS", "true")
	_, failed := summariseApply(strings.NewReader(stream))
	if len(failed) != 1 || strings.Contains(failed[0], standInSiteName) {
		t.Errorf("in a public log the detail must not appear: %v", failed)
	}

	t.Setenv("GITHUB_ACTIONS", "")
	t.Setenv("CI", "")
	_, failed = summariseApply(strings.NewReader(stream))
	if len(failed) != 1 || !strings.Contains(failed[0], "OAuth client is not permitted") {
		t.Errorf("on a workstation the detail must appear, or the error is undebuggable: %v", failed)
	}
}

func TestApplySummaryReportsDiagnosticsWithoutTheirDetail(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	stream := `{"@level":"error","type":"diagnostic","diagnostic":{"severity":"error","summary":"Invalid value for variable","detail":"the site NORTHVALE is not reachable at 10.0.0.1"}}
`
	lines, failed := summariseApply(strings.NewReader(stream))
	joined := strings.Join(append(append([]string{}, lines...), failed...), "\n")

	if strings.Contains(joined, standInSiteName) || strings.Contains(joined, "10.0.0.1") {
		t.Errorf("the diagnostic detail leaked into the summary: %s", joined)
	}
	if len(failed) != 1 || !strings.Contains(failed[0], "Invalid value for variable") {
		t.Errorf("the error summary was dropped, so a failing apply would say nothing: %v", failed)
	}
}

// An unrecognised line is dropped rather than passed through, so a future tofu
// version cannot start leaking by default.
func TestApplySummaryDropsWhatItDoesNotRecognise(t *testing.T) {
	stream := "not json at all, containing NORTHVALE\n" +
		`{"@level":"info","type":"some_future_event","payload":"NORTHVALE"}` + "\n"
	lines, failed := summariseApply(strings.NewReader(stream))
	joined := strings.Join(append(append([]string{}, lines...), failed...), "\n")
	if strings.Contains(joined, standInSiteName) {
		t.Errorf("an unrecognised line was passed through: %s", joined)
	}
}
