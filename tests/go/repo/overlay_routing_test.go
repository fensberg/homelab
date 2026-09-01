package repo

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The hypervisor's network configuration has two properties that a night of
// debugging established the hard way, and neither is visible in any symptom.
// These are hermetic file assertions rather than a health check: they prove
// the playbook still *says* the right thing, which is a different question
// from whether the estate is currently well. See the epoch record.

// A marked reply must be able to reach the local routing table.
//
// The overlay marks packets its daemon originates and restores that mark onto
// every reply. VRF support requires the `local` table to be moved off priority
// 0 to 32765. The overlay's own rules land at 5210-5250, before that - so a
// marked reply for the host's own address is looked up in tables that hold no
// local route, and is discarded on the way in while the socket waits.
//
// The fix is one rule at a priority below the overlay's. This asserts it is
// still there and still ordered correctly, because a rule at the wrong
// priority is indistinguishable from no rule at all, and the failure it
// produces looks like a broken cluster rather than a routing rule.
func TestMarkedRepliesReachTheLocalTable(t *testing.T) {
	body := readHypervisorPlaybook(t)

	if !strings.Contains(body, "fwmark 0x80000/0xff0000 lookup local") {
		t.Fatal("the playbook no longer adds a rule sending the overlay daemon's " +
			"own marked replies to the local table.\n" +
			"Without it a marked reply is routed to a table with no local route " +
			"and never reaches the socket, and the host silently leaves the overlay.")
	}

	// The priority has to be below the overlay's own rules, which start at
	// 5210. Above them, the reply is matched and discarded before this rule
	// is ever consulted.
	pref := regexp.MustCompile(`pref (\d+) from all fwmark 0x80000/0xff0000 lookup local`)
	m := pref.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("the local-table rule is present but its priority could not be read")
	}
	got, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("unreadable priority %q: %v", m[1], err)
	}
	const overlayFirstRule = 5210
	if got >= overlayFirstRule {
		t.Errorf("the local-table rule is at priority %d, which is not before the "+
			"overlay's own rules at %d.\n"+
			"A marked reply is matched by those first and discarded, so this rule "+
			"never runs and the host leaves the overlay.", got, overlayFirstRule)
	}
}

// Every hypervisor has this collision, not just the first one.
//
// The VRF comes with the SDN and the marks come with the overlay, so any host
// running both has it. The fix must therefore be applied per host by the
// playbook rather than recorded once against one machine - which is what makes
// it survive a second hypervisor in this site, or a second site entirely.
func TestTheRoutingFixIsAppliedPerHostNotPerSite(t *testing.T) {
	body := readHypervisorPlaybook(t)

	// It is applied both to the running daemon and by a drop-in, so a restart
	// or a crash does not silently drop the host off the overlay.
	for _, want := range []string{
		"tailscaled.service.d",
		"ExecStartPost=",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the routing fix does not survive a daemon restart: %q is absent.\n"+
				"Applied only to the running host, it is lost on the next restart and "+
				"the host leaves the overlay with nothing reporting it.", want)
		}
	}
}

// IPv6 must be absent rather than half-present.
//
// A stack that is enabled with no addresses on it is worse than either end:
// clients see an IPv6-capable host, prefer it, and fail with EADDRNOTAVAIL.
// That took the overlay's control plane down for hours.
func TestIPv6IsDisabledAtTheStack(t *testing.T) {
	body := readHypervisorPlaybook(t)
	for _, want := range []string{
		"net.ipv6.conf.all.disable_ipv6",
		"net.ipv6.conf.default.disable_ipv6",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("%s is no longer set.\n"+
				"An enabled IPv6 stack with no addresses makes clients prefer a family "+
				"they cannot send from; the absence has to be honest.", want)
		}
	}
}

func readHypervisorPlaybook(t *testing.T) string {
	t.Helper()
	path := filepath.Join(repoRoot(t), "management", "hypervisor", "hypervisor-prep.yml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the hypervisor playbook: %v", err)
	}
	return string(b)
}
