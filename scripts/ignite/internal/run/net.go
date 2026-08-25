package run

import (
	"net"
	"os/exec"
	"strconv"
	"time"
)

// TestPort attempts a single TCP connection, matching what the Verify phase
// needs to know: is anything listening yet.
func TestPort(host string, port int, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// WaitForPort polls a TCP port until it opens or the timeout expires.
func WaitForPort(host string, port int, timeout, interval time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if TestPort(host, port, 5*time.Second) {
			return true
		}
		time.Sleep(interval)
	}
	return false
}

// Ping shells out to the system ping binary rather than crafting raw ICMP,
// which would need elevated capabilities this program has no other reason
// to hold. Matches Test-Connection's two-probe check.
func Ping(host string) bool {
	return exec.Command("ping", "-c", "2", "-W", "2", host).Run() == nil
}
