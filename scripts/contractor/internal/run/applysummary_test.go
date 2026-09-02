package run

import (
	"io"
	"strings"
	"testing"
	"time"
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
	lines, failed := summariseApply(strings.NewReader(recordedApplyStream), nil)

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
	_, failed := summariseApply(strings.NewReader(stream), nil)
	if len(failed) != 1 || strings.Contains(failed[0], standInSiteName) {
		t.Errorf("in a public log the detail must not appear: %v", failed)
	}

	t.Setenv("GITHUB_ACTIONS", "")
	t.Setenv("CI", "")
	_, failed = summariseApply(strings.NewReader(stream), nil)
	if len(failed) != 1 || !strings.Contains(failed[0], "OAuth client is not permitted") {
		t.Errorf("on a workstation the detail must appear, or the error is undebuggable: %v", failed)
	}
}

func TestApplySummaryReportsDiagnosticsWithoutTheirDetail(t *testing.T) {
	t.Setenv("GITHUB_ACTIONS", "true")
	stream := `{"@level":"error","type":"diagnostic","diagnostic":{"severity":"error","summary":"Invalid value for variable","detail":"the site NORTHVALE is not reachable at 10.0.0.1"}}
`
	lines, failed := summariseApply(strings.NewReader(stream), nil)
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
	lines, failed := summariseApply(strings.NewReader(stream), nil)
	joined := strings.Join(append(append([]string{}, lines...), failed...), "\n")
	if strings.Contains(joined, standInSiteName) {
		t.Errorf("an unrecognised line was passed through: %s", joined)
	}
}

// A long destroy must say what it is doing while it is doing it.
//
// Two defects met here, both introduced by routing `tofu destroy` through this
// summary instead of streaming it. The summary dropped apply_progress - tofu's
// "Still destroying... [30s elapsed]" - and the caller collected every line and
// printed them after the scan finished, which for a single long invocation is
// when tofu exits. Between them, a destroy of a whole estate printed nothing at
// all until it was over.
//
// That matters more here than anywhere else in this program: a destroy is the
// one operation that cannot be safely interrupted, and an operator with no
// output cannot tell a ten-minute data source read from a hang.

const recordedDestroyProgress = `{"@level":"info","type":"apply_start","hook":{"resource":{"addr":"proxmox_virtual_environment_vm.talos_cp[0]"},"action":"delete"}}
{"@level":"info","type":"apply_progress","hook":{"resource":{"addr":"proxmox_virtual_environment_vm.talos_cp[0]"},"action":"delete","elapsed_seconds":30}}
{"@level":"info","type":"apply_complete","hook":{"resource":{"addr":"proxmox_virtual_environment_vm.talos_cp[0]"},"action":"delete"}}
{"@level":"info","type":"change_summary","changes":{"add":0,"change":0,"remove":1}}`

func TestProgressIsReported(t *testing.T) {
	lines, _ := summariseApply(strings.NewReader(recordedDestroyProgress), nil)
	var progress string
	for _, l := range lines {
		if strings.HasPrefix(l, "still") {
			progress = l
		}
	}
	if progress == "" {
		t.Fatal("no progress line: a destroy that takes ten minutes would print nothing between its first resource and its last")
	}
	if !strings.Contains(progress, "30s elapsed") {
		t.Errorf("progress line %q does not say how long it has been waiting", progress)
	}
	if !strings.Contains(progress, "talos_cp[0]") {
		t.Errorf("progress line %q does not say which resource it is waiting on", progress)
	}
}

// Progress carries an address and a duration. It must not start carrying
// anything else: this output reaches a public Actions log.
func TestProgressReportsNoValues(t *testing.T) {
	const withAttributes = `{"@level":"info","type":"apply_progress","hook":{"resource":{"addr":"talos_machine_secrets.this"},"action":"delete","elapsed_seconds":10},"cluster_id":"djDvN1ypnafQ","ca_certificate":"LS0tLS1CRUdJTiBD"}`
	lines, _ := summariseApply(strings.NewReader(withAttributes), nil)
	for _, l := range lines {
		if strings.Contains(l, "djDvN1ypnafQ") || strings.Contains(l, "LS0tLS1CRUdJTiBD") {
			t.Fatalf("a value reached the summary: %q", l)
		}
	}
}

// Every line must reach the caller as it is read, not in a batch at the end.
// This is what the phase's output actually depends on, and it is invisible to
// a test that only inspects the returned slice.
func TestLinesAreEmittedAsTheyArrive(t *testing.T) {
	var seen []string
	done := make(chan struct{})
	pr, pw := io.Pipe()

	go func() {
		summariseApply(pr, func(l string) { seen = append(seen, l) })
		close(done)
	}()

	_, _ = pw.Write([]byte(`{"@level":"info","type":"apply_start","hook":{"resource":{"addr":"a.b"},"action":"delete"}}` + "\n"))
	// The writer is deliberately still open: this is the state a long destroy
	// spends its whole life in.
	deadline := time.Now().Add(2 * time.Second)
	for len(seen) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(seen) == 0 {
		pw.Close()
		<-done
		t.Fatal("nothing was emitted while the stream was still open, so a long-running destroy prints its first line only once it has finished")
	}
	pw.Close()
	<-done
}

// Started, finished and failed must not look the same.
//
// All three event types were rendered as "<action> <address>", so every
// resource appeared twice and a failure was indistinguishable from a success.
// A real teardown printed five VMs deleting, five VMs "deleting" again, and
// then reported that the destroy had failed - with nothing anywhere to say
// which five had actually gone. The operator was left to check Proxmox by hand
// against a list the program already had.
func TestAFailedResourceDoesNotLookLikeAFinishedOne(t *testing.T) {
	const stream = `{"@level":"info","type":"apply_start","hook":{"resource":{"addr":"proxmox_virtual_environment_vm.talos_cp[0]"},"action":"delete"}}
{"@level":"info","type":"apply_complete","hook":{"resource":{"addr":"proxmox_virtual_environment_vm.talos_cp[0]"},"action":"delete"}}
{"@level":"info","type":"apply_start","hook":{"resource":{"addr":"proxmox_virtual_environment_vm.talos_cp[1]"},"action":"delete"}}
{"@level":"info","type":"apply_errored","hook":{"resource":{"addr":"proxmox_virtual_environment_vm.talos_cp[1]"},"action":"delete"}}`

	lines, _ := summariseApply(strings.NewReader(stream), nil)

	var done, failed string
	for _, l := range lines {
		if strings.Contains(l, "talos_cp[0]") && strings.Contains(l, "done") {
			done = l
		}
		if strings.Contains(l, "talos_cp[1]") && strings.Contains(l, "FAILED") {
			failed = l
		}
	}
	if done == "" {
		t.Error("the resource that completed is not reported as done")
	}
	if failed == "" {
		t.Fatalf("the resource that errored is not marked as failed; lines were %v", lines)
	}
	if strings.Fields(done)[0] == strings.Fields(failed)[0] {
		t.Errorf("a finished resource and a failed one carry the same label %q, so a teardown that half worked reads as one that worked",
			strings.Fields(done)[0])
	}
}
