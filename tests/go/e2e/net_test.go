//go:build e2e

package e2e_test

import (
	"net"
	"time"
)

// portOpen is the same single-TCP-dial check run.TestPort makes: is anything
// listening yet.
func portOpen(host, port string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
