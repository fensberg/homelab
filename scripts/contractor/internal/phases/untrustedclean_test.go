package phases

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"homelab/details/capacity"
	"homelab/details/hypervisorapi"
)

// untrustedSite is a hypervisor and a cluster that remember what was done to
// them. A machine that is started comes back as one started since.
type untrustedSite struct {
	clean map[string]bool
	nodes map[string]*untrustedNode
	did   []string
	fail  string
	away  bool
}

func (z *untrustedSite) List() ([]hypervisorapi.Guest, error) {
	return []hypervisorapi.Guest{{ID: 1, Name: "lot-dmz-100", Node: "h"}, {ID: 2, Name: "lot-dmz-101", Node: "h"}, {ID: 3, Name: "lot-wk-200", Node: "h"}}, nil
}
func (z *untrustedSite) HasClean(g hypervisorapi.Guest) (bool, error) { return z.clean[g.Name], nil }
func (z *untrustedSite) act(what, name string) error {
	z.did = append(z.did, what+" "+name)
	if z.fail == what {
		return errors.New("refused")
	}
	return nil
}
func (z *untrustedSite) Shutdown(g hypervisorapi.Guest) error  { return z.act("shutdown", g.Name) }
func (z *untrustedSite) TakeClean(g hypervisorapi.Guest) error { return z.act("snapshot", g.Name) }
func (z *untrustedSite) Start(g hypervisorapi.Guest) error {
	if !z.away {
		z.nodes[g.Name].boot += "'"
	}
	return z.act("start", g.Name)
}
func (z *untrustedSite) Node(name string) (untrustedNode, error) { return *z.nodes[name], nil }
func (z *untrustedSite) Forget(name string) error                { return z.act("forget", name) }
func (z *untrustedSite) Open(name string) error                  { return z.act("open", name) }

func untrusted() *untrustedSite {
	backPause, backLooks = 0, 2
	return &untrustedSite{
		clean: map[string]bool{"lot-dmz-101": true},
		nodes: map[string]*untrustedNode{
			"lot-dmz-100": {ready: true, closed: true, boot: "a"},
			"lot-dmz-101": {ready: true, boot: "b"},
		},
	}
}

// The new machines are the site's untrusted ones with no clean snapshot,
// and no other machine: not one that has its snapshot, and not a machine
// that is not in a zone at all. One the hypervisor does not have is an
// error and not a machine with nothing to do.
func TestTheNewUntrustedMachinesAreTheOnesWithNoCleanSnapshot(t *testing.T) {
	z := untrusted()
	fresh, err := withoutASnapshot(z, []string{"lot-dmz-100", "lot-dmz-101"})
	if err != nil || len(fresh) != 1 || fresh[0].Name != "lot-dmz-100" {
		t.Fatalf("the new machines were read as %+v (%v)", fresh, err)
	}
	if _, err := withoutASnapshot(z, []string{"lot-dmz-102"}); err == nil {
		t.Error("a machine the hypervisor does not have was passed over")
	}
}

// A new machine is shut down, snapshotted, started, and opened to work
// only once it is back as a machine started since - in that order, and the
// machine that already has its snapshot is not touched. Its predecessor's
// node is forgotten by name.
func TestANewUntrustedMachineIsGivenItsSnapshotAndOnlyThenOpened(t *testing.T) {
	z := untrusted()
	fresh, _ := withoutASnapshot(z, []string{"lot-dmz-100", "lot-dmz-101"})
	if err := forgetReplaced(z, fresh, z); err != nil {
		t.Fatal(err)
	}
	if err := cleanNew(z, fresh, z); err != nil {
		t.Fatal(err)
	}
	want := "forget lot-dmz-100\nshutdown lot-dmz-100\nsnapshot lot-dmz-100\nstart lot-dmz-100\nopen lot-dmz-100"
	if got := strings.Join(z.did, "\n"); got != want {
		t.Errorf("did:\n%s\nwant:\n%s", got, want)
	}
}

// What is never done: a snapshot of a machine that is open to work, which
// may have run something; and opening a machine whose snapshot failed, or
// that did not come back.
func TestAMachineThatMayHaveRunSomethingIsNotSnapshottedAndAFailedOneIsNotOpened(t *testing.T) {
	open := untrusted()
	open.nodes["lot-dmz-100"].closed = false
	fresh, _ := withoutASnapshot(open, []string{"lot-dmz-100"})
	if err := cleanNew(open, fresh, open); err == nil || len(open.did) != 0 {
		t.Errorf("a machine open to work with no snapshot was snapshotted, or passed: %v %v", err, open.did)
	}
	for name, z := range map[string]*untrustedSite{
		"the snapshot fails":    {fail: "snapshot"},
		"it does not come back": {away: true},
	} {
		base := untrusted()
		z.clean, z.nodes = base.clean, base.nodes
		fresh, _ := withoutASnapshot(z, []string{"lot-dmz-100"})
		if err := cleanNew(z, fresh, z); err == nil || strings.Contains(strings.Join(z.did, " "), "open") {
			t.Errorf("%s: the machine was opened to work, or the failure was not said: %v %v", name, err, z.did)
		}
	}
}

// A node is closed while it carries the taint it joined with, and is back
// when it is ready under a different start than before.
func TestANodeIsReadForItsTaintItsStartAndWhetherItIsReady(t *testing.T) {
	got, err := untrustedNodeFrom([]byte(fmt.Sprintf(`{"spec":{"taints":[{"key":"other"},{"key":%q}]},
		"status":{"nodeInfo":{"bootID":"b-7"},"conditions":[{"type":"Ready","status":"True"}]}}`, capacity.NotYetClean)))
	if err != nil || got != (untrustedNode{ready: true, closed: true, boot: "b-7"}) {
		t.Errorf("the node was read as %+v (%v)", got, err)
	}
	got, err = untrustedNodeFrom([]byte(`{"spec":{"taints":[{"key":"other"}]},"status":{"conditions":[{"type":"Ready","status":"False"}]}}`))
	if err != nil || got != (untrustedNode{}) {
		t.Errorf("a node that is open and not ready was read as %+v (%v)", got, err)
	}
	if _, err := untrustedNodeFrom([]byte("not json")); err == nil {
		t.Error("what is not a node's description was read as one")
	}
}
