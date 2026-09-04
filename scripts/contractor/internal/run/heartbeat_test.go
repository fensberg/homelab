package run

import (
	"io"
	"strings"
	"testing"
	"time"
)

// A long read must not be silent.
//
// Observed on a real ignition: the cluster health data source printed
// `still data.talos_cluster_health.this (10s elapsed)` and then nothing at all
// until it finished. tofu emits apply_progress for a resource being applied and
// not for a data source being read, so the fix that gave long destroys a voice
// never covered reads - and an operator watching a ten-minute ceiling cannot
// tell a slow read from a hang.
//
// The beat is driven by the test rather than by the clock. Two tests in this
// repository have already failed on a different machine for reading the
// machine, and a test that sleeps for thirty seconds to assert a heartbeat is
// a test nobody runs.
func TestASilentReadStillReportsItself(t *testing.T) {
	// One progress event, then a stream that stays open and says nothing.
	stream, writer := blockingStream(t, `{"type":"apply_progress","hook":{"resource":{"addr":"data.talos_cluster_health.this"},"elapsed_seconds":10}}`)
	defer writer()

	tick := make(chan time.Time)
	said := make(chan string, 16)

	go summariseStream(stream, func(line string) { said <- line }, tick)

	if first := <-said; !strings.Contains(first, "still") {
		t.Fatalf("expected tofu's own progress line first, got %q", first)
	}

	// First beat: something was said since the last one, so stay quiet.
	tick <- time.Now()
	// Second beat: nothing has been said, so speak.
	tick <- time.Now()

	select {
	case line := <-said:
		if !strings.Contains(line, "waiting") {
			t.Errorf("the beat did not report a wait: %q", line)
		}
		if !strings.Contains(line, "data.talos_cluster_health.this") {
			t.Errorf("the beat does not name what is outstanding: %q", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the stream went silent and nothing reported it, which is the defect this exists to fix")
	}
}

// The wait says what it is expected to cost.
//
// Silence is alarming; silence with "this usually takes about two minutes" is
// information. Measured on the ignition of 2026-09-04 rather than guessed.
func TestASlowReadSaysWhatItIsExpectedToCost(t *testing.T) {
	stream, writer := blockingStream(t, `{"type":"apply_progress","hook":{"resource":{"addr":"data.talos_cluster_health.this"},"elapsed_seconds":10}}`)
	defer writer()

	tick := make(chan time.Time)
	said := make(chan string, 16)
	go summariseStream(stream, func(line string) { said <- line }, tick)

	<-said
	tick <- time.Now()
	tick <- time.Now()

	line := <-said
	for _, want := range []string{"two minutes", "gives up at ten"} {
		if !strings.Contains(line, want) {
			t.Errorf("the beat does not carry %q: %q", want, line)
		}
	}
}

// A beat beside a line tofu just printed is noise, and noise is how a progress
// line stops being read.
func TestTheBeatStaysQuietWhileTofuIsTalking(t *testing.T) {
	stream, writer := blockingStream(t, `{"type":"apply_progress","hook":{"resource":{"addr":"a.b"},"elapsed_seconds":10}}`)
	defer writer()

	tick := make(chan time.Time)
	said := make(chan string, 16)
	go summariseStream(stream, func(line string) { said <- line }, tick)

	<-said // tofu's line

	tick <- time.Now() // something was said since the last beat: stay quiet
	select {
	case line := <-said:
		t.Errorf("the beat spoke over tofu's own output: %q", line)
	case <-time.After(100 * time.Millisecond):
	}
}

// A stream that ends returns, rather than beating forever.
func TestTheStreamEndingEndsTheLoop(t *testing.T) {
	done := make(chan struct{})
	go func() {
		summariseStream(strings.NewReader(`{"type":"apply_progress","hook":{"resource":{"addr":"a.b"},"elapsed_seconds":10}}`+"\n"), nil, make(chan time.Time))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the loop did not return when the stream ended")
	}
}

// blockingStream yields the given lines and then stays open, which is what a
// tofu process does while it waits on something slow.
//
// A strings.Reader will not do: it reaches EOF immediately, the loop returns,
// and the beat is never reached. The first version of this test did exactly
// that and hung on a tick nobody was left to receive - which is a fair
// imitation of the bug being fixed, and no use as a test.
func blockingStream(t *testing.T, lines ...string) (io.Reader, func()) {
	t.Helper()
	pr, pw := io.Pipe()
	go func() {
		_, _ = io.WriteString(pw, strings.Join(lines, "\n")+"\n")
	}()
	return pr, func() { _ = pw.Close() }
}
