package run

import (
	"net"
	"testing"
	"time"
)

// The Verify and Compute phases decide whether infrastructure is up entirely
// on the strength of these two functions. Compute blocks for five minutes on
// WaitForPort before declaring a node dead and printing a diagnosis; Verify
// refuses to start OpenTofu at all if TestPort says no. Both are testable
// against a listener on localhost with no infrastructure of any kind, so
// there is no excuse for either being untested.

// listener opens a real TCP listener on a free port and returns its address.
// Port 0 lets the kernel choose, so parallel tests never collide.
func listener(t *testing.T) (host string, port int, close func()) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("opening a listener: %v", err)
	}
	addr := l.Addr().(*net.TCPAddr)
	return "127.0.0.1", addr.Port, func() { _ = l.Close() }
}

func TestTestPort_OpenPort(t *testing.T) {
	t.Parallel()
	host, port, close := listener(t)
	defer close()

	if !TestPort(host, port, 2*time.Second) {
		t.Errorf("TestPort said no on a port that is definitely listening")
	}
}

func TestTestPort_ClosedPort(t *testing.T) {
	t.Parallel()
	// Opened and immediately closed, so the port is known to have been free
	// and is now known to have nothing on it - more reliable than picking a
	// number and hoping.
	host, port, close := listener(t)
	close()

	if TestPort(host, port, 2*time.Second) {
		t.Errorf("TestPort said yes on a closed port")
	}
}

func TestWaitForPort_ReturnsAsSoonAsThePortOpens(t *testing.T) {
	t.Parallel()
	host, port, close := listener(t)
	defer close()

	start := time.Now()
	if !WaitForPort(host, port, 5*time.Second, 100*time.Millisecond) {
		t.Fatal("WaitForPort timed out on an already-open port")
	}
	// It must not sleep through a full interval before its first attempt:
	// Compute calls this once per node, and a poll-then-check ordering would
	// add the interval to every single ignition run for no reason.
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("WaitForPort took %s on an already-open port; it should probe before it sleeps", elapsed)
	}
}

func TestWaitForPort_GivesUpAtTheDeadline(t *testing.T) {
	t.Parallel()
	host, port, close := listener(t)
	close()

	start := time.Now()
	if WaitForPort(host, port, 300*time.Millisecond, 50*time.Millisecond) {
		t.Fatal("WaitForPort said yes on a closed port")
	}
	// The bound is generous: what is being tested is that it returns at all
	// rather than running to the 5-minute timeout Compute passes it.
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("WaitForPort took %s to honour a 300ms timeout", elapsed)
	}
}
