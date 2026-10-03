package phases

import (
	"strings"
	"testing"
)

// nodesJSON is `kubectl get nodes -o json` for a site with three control
// planes and two workers, as far as this reads it.
const nodesJSON = `{"items": [
  {"metadata": {"name": "site0-cp-100", "labels": {"node-role.kubernetes.io/control-plane": ""}},
   "status": {"addresses": [{"type": "Hostname", "address": "site0-cp-100"}, {"type": "InternalIP", "address": "10.0.10.100"}]}},
  {"metadata": {"name": "site0-cp-101", "labels": {"node-role.kubernetes.io/control-plane": ""}},
   "status": {"addresses": [{"type": "InternalIP", "address": "10.0.10.101"}]}},
  {"metadata": {"name": "site0-cp-102", "labels": {"node-role.kubernetes.io/control-plane": ""}},
   "status": {"addresses": [{"type": "InternalIP", "address": "10.0.10.102"}]}},
  {"metadata": {"name": "site0-worker-200", "labels": {}},
   "status": {"addresses": [{"type": "InternalIP", "address": "10.0.10.200"}]}},
  {"metadata": {"name": "site0-worker-201"},
   "status": {"addresses": [{"type": "InternalIP", "address": "10.0.10.201"}]}}
]}`

func TestTheClustersMachinesAreReadWithTheirAddressAndKind(t *testing.T) {
	got, err := parseMachines([]byte(nodesJSON))
	if err != nil {
		t.Fatal(err)
	}
	want := []machine{
		{Name: "site0-cp-100", IP: "10.0.10.100", ControlPlane: true},
		{Name: "site0-cp-101", IP: "10.0.10.101", ControlPlane: true},
		{Name: "site0-cp-102", IP: "10.0.10.102", ControlPlane: true},
		{Name: "site0-worker-200", IP: "10.0.10.200"},
		{Name: "site0-worker-201", IP: "10.0.10.201"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d machines, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("machine %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

// A machine whose address cannot be read cannot be compared with the config,
// and a machine that cannot be compared is not known to be staying.
func TestAMachineWithNoAddressIsAnErrorAndNotAMachineThatStays(t *testing.T) {
	_, err := parseMachines([]byte(`{"items": [{"metadata": {"name": "site0-cp-100"}, "status": {"addresses": []}}]}`))
	if err == nil || !strings.Contains(err.Error(), "site0-cp-100") {
		t.Fatalf("got %v, want an error naming the machine", err)
	}
}

func TestAClusterThatListsNoMachinesIsAnError(t *testing.T) {
	if _, err := parseMachines([]byte(`{"items": []}`)); err == nil {
		t.Fatal("an empty list was read as a cluster with nothing leaving")
	}
}

func fiveAndTwo() []machine {
	return []machine{
		{Name: "site0-cp-100", IP: "10.0.10.100", ControlPlane: true},
		{Name: "site0-cp-101", IP: "10.0.10.101", ControlPlane: true},
		{Name: "site0-cp-102", IP: "10.0.10.102", ControlPlane: true},
		{Name: "site0-cp-103", IP: "10.0.10.103", ControlPlane: true},
		{Name: "site0-cp-104", IP: "10.0.10.104", ControlPlane: true},
		{Name: "site0-worker-200", IP: "10.0.10.200"},
		{Name: "site0-worker-201", IP: "10.0.10.201"},
	}
}

func machineNames(ms []machine) string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Name
	}
	return strings.Join(out, " ")
}

// The epoch's acceptance test, as a decision: five control planes, and a
// config that asks for three.
func TestAMachineTheConfigNoLongerAsksForIsLeaving(t *testing.T) {
	wanted := []string{"10.0.10.100", "10.0.10.101", "10.0.10.102", "10.0.10.200", "10.0.10.201"}
	if got := machineNames(leaving(fiveAndTwo(), wanted)); got != "site0-cp-103 site0-cp-104" {
		t.Fatalf("leaving: %q", got)
	}
}

func TestNothingLeavesWhenTheConfigAsksForEveryMachine(t *testing.T) {
	var wanted []string
	for _, m := range fiveAndTwo() {
		wanted = append(wanted, m.IP)
	}
	// A machine the config asks for and the cluster does not have yet is the
	// compute phase's to build, and nothing here.
	wanted = append(wanted, "10.0.10.202")
	if got := leaving(fiveAndTwo(), wanted); len(got) != 0 {
		t.Fatalf("leaving: %q", machineNames(got))
	}
}

// Workers go first: retiring one cannot move quorum, so the control plane is
// whole while work is being moved.
func TestWorkersLeaveBeforeControlPlanes(t *testing.T) {
	wanted := []string{"10.0.10.100", "10.0.10.101", "10.0.10.102", "10.0.10.200"}
	if got := machineNames(leaving(fiveAndTwo(), wanted)); got != "site0-worker-201 site0-cp-103 site0-cp-104" {
		t.Fatalf("leaving: %q", got)
	}
}

func TestTheJobsOwnMachineIsHandedOverAndTheRestAreRetiredNow(t *testing.T) {
	out := []machine{{Name: "site0-worker-200"}, {Name: "site0-worker-201"}, {Name: "site0-cp-104", ControlPlane: true}}

	now, handed := exceptOwn(out, "site0-worker-201")
	if machineNames(now) != "site0-worker-200 site0-cp-104" {
		t.Errorf("retired now: %q", machineNames(now))
	}
	if handed == nil || handed.Name != "site0-worker-201" {
		t.Errorf("handed over: %+v", handed)
	}
}

func TestNothingIsHandedOverWhenTheJobsMachineIsStaying(t *testing.T) {
	out := []machine{{Name: "site0-cp-103", ControlPlane: true}, {Name: "site0-cp-104", ControlPlane: true}}
	for _, own := range []string{"site0-worker-200", ""} {
		now, handed := exceptOwn(out, own)
		if machineNames(now) != "site0-cp-103 site0-cp-104" || handed != nil {
			t.Errorf("own %q: retired now %q, handed over %+v", own, machineNames(now), handed)
		}
	}
}

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

// Run from a workstation, the converge is on no machine of the cluster.
func TestAConvergeOutsideAClusterIsOnNoMachine(t *testing.T) {
	own, err := ownMachine(env(nil))
	if err != nil || own != "" {
		t.Fatalf("got %q, %v", own, err)
	}
}

func TestAConvergeInAClusterIsOnTheMachineItsPodWasTold(t *testing.T) {
	own, err := ownMachine(env(map[string]string{"KUBERNETES_SERVICE_HOST": "10.96.0.1", ownMachineVar: "site0-worker-200"}))
	if err != nil || own != "site0-worker-200" {
		t.Fatalf("got %q, %v", own, err)
	}
}

// Inside a cluster and not told which machine: it may be on one that is
// leaving, and retiring that one ends the job with the estate half changed.
func TestAConvergeInAClusterThatCannotSayWhereItIsRefuses(t *testing.T) {
	_, err := ownMachine(env(map[string]string{"KUBERNETES_SERVICE_HOST": "10.96.0.1"}))
	if err == nil || !strings.Contains(err.Error(), ownMachineVar) {
		t.Fatalf("got %v, want a refusal naming %s", err, ownMachineVar)
	}
}
