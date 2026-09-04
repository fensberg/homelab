package phases

import (
	"errors"
	"strings"
	"testing"
	"time"

	"homelab/contractor/internal/run"
)

// A tool that is not installed must not be retried, and must not be reported
// as a sick cluster.
//
// Both halves failed together on 2026-09-03: talosctl was absent from the
// runner, the check retried it for the full five minutes, and the banner said
// "etcd membership did not become healthy within 5m0s" about an etcd nothing
// had looked at. The wrong verdict then fed a workflow that decided to revert
// an entire epoch out of the repository on the strength of it.
func TestAnUnavailableToolFailsImmediatelyAndBlamesTheTool(t *testing.T) {
	ctx := &run.Context{Site: "site0"}
	var attempts int

	started := time.Now()
	err := waitFor(ctx, "", "etcd membership", 5*time.Minute, func(*run.Context, string) error {
		attempts++
		return &Unavailable{Tool: "talosctl", Why: "not on PATH, so etcd membership was never measured"}
	})
	elapsed := time.Since(started)

	if err == nil {
		t.Fatal("an unavailable tool was treated as a healthy cluster")
	}
	if attempts != 1 {
		t.Errorf("the check ran %d times; an absent binary cannot become present by waiting", attempts)
	}
	if elapsed > 5*time.Second {
		t.Errorf("waited %s before giving up on a deterministic failure", elapsed)
	}

	msg := err.Error()
	if !strings.Contains(msg, "talosctl") {
		t.Error("the message does not name the tool that was missing")
	}
	if !strings.Contains(msg, "could not be checked") {
		t.Error("the message does not say the question went unasked")
	}
	// The specific wrong sentence, named so it cannot come back.
	if strings.Contains(msg, "did not become healthy") {
		t.Error("the message still reports a verdict about the cluster; nothing was measured")
	}
	if !strings.Contains(msg, "NOT a verdict") {
		t.Error("the message does not make clear that nothing is known about the estate")
	}
}

// The ordinary path is untouched: a real failure still waits, and still says
// what never arrived.
func TestARealFailureIsStillRetriedUntilTheDeadline(t *testing.T) {
	ctx := &run.Context{Site: "site0"}
	var attempts int

	err := waitFor(ctx, "", "nodes", 10*time.Millisecond, func(*run.Context, string) error {
		attempts++
		return errors.New("2 of 3 nodes are Ready")
	})

	if err == nil {
		t.Fatal("a failing check passed")
	}
	if attempts < 1 {
		t.Fatal("the check never ran")
	}
	if !strings.Contains(err.Error(), "did not become healthy") {
		t.Errorf("an ordinary failure lost its message: %v", err)
	}
	if !strings.Contains(err.Error(), "2 of 3 nodes are Ready") {
		t.Error("the failure no longer names what never arrived")
	}
}

// Unavailable has to survive being wrapped, because a check that adds context
// on the way out is the normal case rather than the exception.
func TestUnavailableIsFoundThroughAWrap(t *testing.T) {
	ctx := &run.Context{Site: "site0"}
	err := waitFor(ctx, "", "etcd membership", time.Minute, func(*run.Context, string) error {
		return errors.Join(errors.New("reading membership"), &Unavailable{Tool: "talosctl", Why: "not on PATH"})
	})
	if err == nil || !strings.Contains(err.Error(), "could not be checked") {
		t.Errorf("a wrapped Unavailable was treated as an ordinary failure: %v", err)
	}
}

// The message names the tool and says what could not be established.
func TestUnavailableSaysWhichToolAndWhy(t *testing.T) {
	u := &Unavailable{Tool: "talosctl", Why: "not on PATH, so etcd membership was never measured"}
	got := u.Error()
	for _, want := range []string{"talosctl", "unavailable", "never measured"} {
		if !strings.Contains(got, want) {
			t.Errorf("the message %q does not carry %q", got, want)
		}
	}
}
