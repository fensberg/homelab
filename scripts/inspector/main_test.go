package main

import (
	"os"
	"testing"
)

// No test in this package reaches the real GitHub.
//
// The network is an input, and a test states every input it depends on. This
// points the API at a closed port on the loopback: no DNS, no egress, and an
// immediate refusal rather than a timeout. A test that wants an answer stands
// up an httptest server and says so.
func TestMain(m *testing.M) {
	githubAPI = "http://127.0.0.1:1"
	os.Exit(m.Run())
}
