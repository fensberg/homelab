package phases

import (
	"os"
	"testing"
)

// No test in this package reaches the real GitHub.
//
// The pending-deploy check used to shell out to gh, so a test could express
// "cannot ask" by emptying PATH. It asks over HTTP now, and the first version
// of that change left TestIgnitionFailsClosedWhenItCannotAsk making a live
// call to api.github.com from a unit test - which passed locally, failed in
// CI, and would have been a different answer on a different day either way.
//
// The network is an input, and a test states every input it depends on. This
// points the API at a closed port on the loopback by default: no DNS, no
// egress, and an immediate refusal rather than a timeout. A test that wants an
// answer stands up an httptest server and says so.
func TestMain(m *testing.M) {
	githubAPI = "http://127.0.0.1:1"
	os.Exit(m.Run())
}
