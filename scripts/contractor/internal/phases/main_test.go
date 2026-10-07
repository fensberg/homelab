package phases

import (
	"os"
	"testing"

	"homelab/details/gitenv"
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
//
// Nor does any read the developer's git configuration: one builds a
// repository in a temporary directory and commits to it, and a signing
// setting or a hook from the machine would decide whether that works.
func TestMain(m *testing.M) {
	gitenv.Isolate()
	githubAPI = "http://127.0.0.1:1"
	// No test here has a hypervisor to ask. Each is given one host of a
	// plausible shape, and the tests of the asking itself put the real
	// reader back (hardware_test.go).
	surveySite = aSiteWithOneHost
	os.Exit(m.Run())
}
