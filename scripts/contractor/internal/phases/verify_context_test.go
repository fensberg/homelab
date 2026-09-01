package phases

import (
	"strings"
	"testing"
	"time"

	"homelab/contractor/internal/run"
)

// Verify's pre-flight has two callers in genuinely different situations, and
// running one caller's check on the other is what broke the first converge to
// get this far.
//
// Ignition runs from a workstation before any node exists, so the gateway
// answering ICMP is the only proof available. A converge runs from a pod,
// which cannot send ICMP at all - Pod Security Admission requires capabilities
// to be dropped, NET_RAW with them. Measured on this estate: from a pod, ICMP
// to the gateway fails while TCP to a node on the same subnet succeeds, at the
// same moment. So the ping was a gate the converge could never pass, however
// healthy the estate was.

// With no node addresses there is nothing to prove the path against, and
// saying so beats probing an empty list and reporting success.
func TestNoDeclaredNodesIsAnError(t *testing.T) {
	ctx := &run.Context{Site: "site0"}
	err := verifyNodeSubnetReachable(ctx, nil, time.Millisecond)
	if err == nil {
		t.Fatal("an empty node list was treated as a reachable subnet")
	}
	if !strings.Contains(err.Error(), "nothing to prove") {
		t.Errorf("the error does not say why it cannot answer: %v", err)
	}
}

// Unreachable is a failure, and the message has to be actionable without
// printing an address - this runs in a public repository's Actions log.
func TestUnreachableNodesFailWithoutPrintingAddresses(t *testing.T) {
	ctx := &run.Context{Site: "site0"}
	// TEST-NET-1, reserved for documentation and guaranteed not to answer.
	addrs := []string{"192.0.2.10", "192.0.2.11"}

	err := verifyNodeSubnetReachable(ctx, addrs, 10*time.Millisecond)
	if err == nil {
		t.Fatal("unreachable nodes were reported as a working path")
	}
	for _, a := range addrs {
		if strings.Contains(err.Error(), a) {
			t.Errorf("the failure prints %q; this output lands in a public "+
				"Actions log and the addresses come from the vault", a)
		}
	}
	if !strings.Contains(err.Error(), "management.rendered.json") {
		t.Error("the failure does not say where the addresses can be found, " +
			"which is what makes withholding them workable")
	}
}
