// Package tcp answers one question about an address: is anything listening.
//
// Written once. The contractor asked it through run.TestPort and
// run.WaitForPort, and the integration and e2e tiers each carried their own
// portOpen - two of them identical - plus a second waitForPort, four copies of
// one dial in two modules (docs/epochs/02-abstraction.md, "A helper is written
// once"). It lives here because both modules import homelab/details, and
// neither can import the contractor's internal packages.
package tcp

import (
	"net"
	"strconv"
	"time"
)

// dialTimeout bounds each attempt Await makes, so one hung dial cannot use the
// whole wait.
const dialTimeout = 5 * time.Second

// Addr is host and port as a dial address, bracketing an IPv6 host.
func Addr(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// Listening reports whether one TCP connection to addr succeeds within
// timeout. It proves something accepted a connection, nothing about what.
func Listening(addr string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// Await dials addr until something listens or timeout passes, waiting
// interval between attempts. It always makes at least one attempt.
func Await(addr string, timeout, interval time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if Listening(addr, min(dialTimeout, timeout)) {
			return true
		}
		if time.Now().Add(interval).After(deadline) {
			return false
		}
		time.Sleep(interval)
	}
}
