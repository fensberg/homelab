package tcp

import (
	"net"
	"testing"
	"time"
)

// The contractor decides whether infrastructure is up entirely on these:
// Compute blocks for five minutes on Await before declaring a node dead, and
// Verify refuses to start OpenTofu if Listening says no. Both are testable
// against a listener on localhost with no infrastructure at all.

// listener opens a real TCP listener on a free port. Port 0 lets the kernel
// choose, so parallel tests never collide.
func listener(t *testing.T) (addr string, close func()) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("opening a listener: %v", err)
	}
	return l.Addr().String(), func() { _ = l.Close() }
}

func TestListeningOnAnOpenPort(t *testing.T) {
	t.Parallel()
	addr, close := listener(t)
	defer close()
	if !Listening(addr, 2*time.Second) {
		t.Errorf("%s is listening and was reported closed", addr)
	}
}

// Opened and immediately closed, so the port is known to have been free and
// is now known to have nothing on it - more reliable than picking a number.
func TestListeningOnAClosedPort(t *testing.T) {
	t.Parallel()
	addr, close := listener(t)
	close()
	if Listening(addr, 2*time.Second) {
		t.Errorf("%s has nothing listening and was reported open", addr)
	}
}

// It must not sleep through an interval before its first attempt: Compute
// calls this once per node, and sleeping first would add the interval to
// every build for nothing.
func TestAwaitProbesBeforeItSleeps(t *testing.T) {
	t.Parallel()
	addr, close := listener(t)
	defer close()
	start := time.Now()
	if !Await(addr, 5*time.Second, 3*time.Second) {
		t.Fatal("Await timed out on a port that was already open")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Await took %s on an open port; it should probe before it sleeps", elapsed)
	}
}

// A listener that arrives during the wait is found.
func TestAwaitFindsAListenerThatArrivesLater(t *testing.T) {
	t.Parallel()
	addr, close := listener(t)
	close()
	arrived := make(chan net.Listener, 1)
	go func() {
		time.Sleep(300 * time.Millisecond)
		l, err := net.Listen("tcp", addr)
		if err != nil {
			arrived <- nil
			return
		}
		arrived <- l
	}()
	found := Await(addr, 5*time.Second, 100*time.Millisecond)
	if l := <-arrived; l != nil {
		_ = l.Close()
	} else {
		t.Skip("the freed port was taken by something else before the listener could return")
	}
	if !found {
		t.Fatalf("a listener that arrived during the wait on %s was not found", addr)
	}
}

// The bound is generous: what is tested is that it returns at all, rather than
// running to the five minutes Compute passes it.
func TestAwaitGivesUpAtTheDeadline(t *testing.T) {
	t.Parallel()
	addr, close := listener(t)
	close()
	start := time.Now()
	if Await(addr, 300*time.Millisecond, 50*time.Millisecond) {
		t.Fatal("Await reported a closed port open")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("Await took %s to honour a 300ms timeout", elapsed)
	}
}

func TestAddrBracketsAnIPv6Host(t *testing.T) {
	if got := Addr("::1", 53); got != "[::1]:53" {
		t.Errorf("got %q", got)
	}
	if got := Addr("192.0.2.10", 6443); got != "192.0.2.10:6443" {
		t.Errorf("got %q", got)
	}
}
