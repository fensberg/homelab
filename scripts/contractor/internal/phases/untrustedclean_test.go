package phases

import (
	"errors"
	"fmt"
	"homelab/contractor/internal/run"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homelab/contractor/config"
	"homelab/contractor/steps"
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
		"the snapshot fails": {fail: "snapshot"},
		"it never returns":   {away: true},
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

// The cluster as the converge reaches it, through kubectl: a node is read
// as it describes itself, opened by lifting the one taint, and forgotten
// only when there is one to forget.
func TestTheConvergeReadsOpensAndForgetsANodeThroughKubectl(t *testing.T) {
	bin, dir := t.TempDir(), t.TempDir()
	log := filepath.Join(dir, "asked")
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\n" +
		"case \"$*\" in\n" +
		"  'get nodes -o name') echo node/lot-dmz-100; echo node/lot-wk-200 ;;\n" +
		"  'get node lot-dmz-100 -o json') echo '{\"status\":{\"nodeInfo\":{\"bootID\":\"b-1\"},\"conditions\":[{\"type\":\"Ready\",\"status\":\"True\"}]}}' ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(bin, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx := &run.Context{}
	ctx.Dir = dir
	k := kubectlNodes{ctx: ctx, kubeconfig: filepath.Join(dir, "kubeconfig")}

	got, err := k.Node("lot-dmz-100")
	if err != nil || got != (untrustedNode{ready: true, boot: "b-1"}) {
		t.Errorf("the node was read as %+v (%v)", got, err)
	}
	if err := k.Open("lot-dmz-100"); err != nil {
		t.Fatal(err)
	}
	if err := k.Forget("lot-dmz-100"); err != nil {
		t.Fatal(err)
	}
	if err := k.Forget("lot-dmz-101"); err != nil {
		t.Fatal(err)
	}
	asked, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	want := "get node lot-dmz-100 -o json\n" +
		"taint node lot-dmz-100 " + capacity.NotYetClean + "-\n" +
		"get nodes -o name\ndelete node lot-dmz-100\n" +
		"get nodes -o name\n"
	if string(asked) != want {
		t.Errorf("kubectl was asked:\n%s\nwant:\n%s", asked, want)
	}
}

// A new machine's configuration is named by the key the address plan gives
// it, and a machine the plan does not name is an error rather than a
// configuration nobody sends.
func TestANewMachinesConfigurationIsNamedByItsKey(t *testing.T) {
	keys := map[string]string{"lot-dmz-100": "zone0-100"}
	got, err := configurationsOf([]hypervisorapi.Guest{{Name: "lot-dmz-100"}}, keys)
	if want := steps.ZoneConfig + `["zone0-100"]`; err != nil || len(got) != 1 || got[0] != want {
		t.Errorf("got %v (%v), want [%s]", got, err, want)
	}
	if _, err := configurationsOf([]hypervisorapi.Guest{{Name: "lot-dmz-101"}}, keys); err == nil {
		t.Error("a machine the address plan does not name was given no configuration, and no error")
	}
}

// rebuilt is a hypervisor where each of the site's untrusted workers is new
// the first time it is looked at - the converge's step that configures them
// - and has its snapshot by the next look, so the cluster phase's end finds
// nothing left to snapshot.
type rebuilt struct {
	untrustedGuests
	names  []string
	looked map[string]bool
}

func (r *rebuilt) List() ([]hypervisorapi.Guest, error) {
	var out []hypervisorapi.Guest
	for i, n := range r.names {
		out = append(out, hypervisorapi.Guest{ID: i + 1, Name: n, Node: "h"})
	}
	return out, nil
}
func (r *rebuilt) HasClean(g hypervisorapi.Guest) (bool, error) {
	seen := r.looked[g.Name]
	r.looked[g.Name] = true
	return seen, nil
}

// A converge that finds an untrusted worker new - built again in an earlier
// step - sends it its configuration whatever the plan says: that step's
// apply replaces the machine's configuration by name, and no other step's
// apply replaces anything. Planned alone, the step finds nothing changing,
// since the machine was replaced in a step before it, and the machine waits
// unconfigured and never joins.
func TestARebuiltUntrustedWorkerIsSentItsConfigurationAgain(t *testing.T) {
	f := newVerbFixture(t)
	f.ctx.TakenOverOK = true
	cfg, err := config.LoadRendered(f.ctx.ConfigRendered)
	if err != nil {
		t.Fatal(err)
	}
	net, err := config.ResolveSiteNetwork(cfg, f.ctx.Site)
	if err != nil || len(net.DMZNames) == 0 {
		t.Fatalf("the fixture's site has no untrusted worker to rebuild (%v)", err)
	}
	was := untrustedHypervisor
	t.Cleanup(func() { untrustedHypervisor = was })
	site := &rebuilt{names: net.DMZNames, looked: map[string]bool{}}
	untrustedHypervisor = func(*run.Context) (untrustedGuests, []string, error) { return site, net.DMZNames, nil }

	if err := applySteps(f.ctx, "cluster", siteHas{workers: true, zones: true}); err != nil {
		t.Fatal(err)
	}
	tofu, _ := f.calls(t)
	want := "-replace=" + steps.ZoneConfig + `["` + net.DMZKeys[net.DMZNames[0]] + `"]`
	replaced := 0
	for _, c := range tofu {
		if !strings.HasPrefix(c.args, "apply ") || !strings.Contains(c.args, "-replace=") {
			continue
		}
		replaced++
		if !strings.Contains(c.args, "-target="+steps.ZoneConfig) || !strings.Contains(c.args, want) {
			t.Errorf("an apply replaced something, and it was not the zone step replacing the new machine's configuration (%s): %s", want, c.args)
		}
	}
	if replaced != 1 {
		t.Errorf("%d applies replaced a configuration; the zone step's, and only it, is to", replaced)
	}
}
