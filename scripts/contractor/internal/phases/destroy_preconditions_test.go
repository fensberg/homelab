package phases

import (
	"strings"
	"testing"
	"time"

	"homelab/contractor/internal/config"
)

// TNT is TNT. A teardown either goes off or does not start.
//
// The teardown never checked that it could reach the hypervisor. Ignition and
// converge both run Verify first; this one went straight to OpenTofu - and a
// provider asked to work against an unreachable API does not fail, it waits.
// Observed as a teardown that sat for ninety minutes and, when interrupted,
// reported "Plugin did not respond ... ReadResource", which is what a stuck
// outbound call looks like from the other side.
//
// The check has to be before, not during. By the time the destroy runs, the
// object storage has been emptied and cluster-internal resources forgotten out
// of state. A run that stops there has done irreversible work and left machines
// nothing tracks: the same explosive with the fuse half burnt, which is worse
// than one that refused to begin.

func TestTheTeardownChecksTheHypervisorAnswers(t *testing.T) {
	var names []string
	for _, p := range DestroyPreconditions(50 * time.Millisecond) {
		names = append(names, p.name)
	}
	if len(names) == 0 {
		t.Fatal("the teardown has no preconditions, so it will begin against an " +
			"unreachable hypervisor and hang partway through")
	}
	found := false
	for _, n := range names {
		if strings.Contains(n, "hypervisor") {
			found = true
		}
	}
	if !found {
		t.Errorf("preconditions are %v, none of which is the hypervisor's API - which is "+
			"the one every provider call in the teardown depends on", names)
	}
}

// A site with no hypervisor is refused rather than probed. Reading "nothing
// declared" as "nothing to check" is how a guard passes on an empty input.
func TestPreconditionsRefuseASiteWithNoHypervisor(t *testing.T) {
	for _, p := range DestroyPreconditions(50 * time.Millisecond) {
		if err := p.check(&config.SiteNetwork{}); err == nil {
			t.Errorf("precondition %q passed against a site declaring no hypervisor", p.name)
		}
	}
}

// An unreachable hypervisor must say so in terms an operator can act on, and
// without printing the address - this output gets pasted into issues, and the
// address comes from the vault like every other value here.
func TestAnUnreachableHypervisorIsExplainedWithoutNamingIt(t *testing.T) {
	const address = "192.0.2.1" // RFC 5737, and nothing listens on it
	net := config.SiteNetwork{
		Hypervisors: []config.Node{{Hostname: "example", IP: address}},
	}

	var got error
	for _, p := range DestroyPreconditions(50 * time.Millisecond) {
		if err := p.check(&net); err != nil {
			got = err
		}
	}
	if got == nil {
		t.Fatal("an unreachable hypervisor passed the precondition, so the teardown would " +
			"begin and hang inside a provider")
	}
	msg := got.Error()
	if strings.Contains(msg, address) {
		t.Errorf("the failure names the hypervisor's address:\n%s", msg)
	}
	if !strings.Contains(msg, "Nothing has been destroyed") {
		t.Errorf("the failure does not say the estate is untouched, which is the first "+
			"thing somebody reading it needs to know:\n%s", msg)
	}
}
