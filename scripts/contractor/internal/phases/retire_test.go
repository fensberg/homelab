package phases

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homelab/contractor/config"
	"homelab/contractor/internal/run"
	"homelab/details/applications"
)

// What kubectl prints for a site with three control planes and two workers:
// a line per machine, and the control planes' names.
const (
	machineListing = `site0-cp-100 10.0.10.100
site0-cp-101 10.0.10.101
site0-cp-102 10.0.10.102
site0-worker-200 10.0.10.200
site0-worker-201 10.0.10.201
`
	controlPlaneListing = "site0-cp-100 site0-cp-101 site0-cp-102"
)

func TestTheClustersMachinesAreReadWithTheirAddressAndKind(t *testing.T) {
	got, err := parseMachines(machineListing, controlPlaneListing)
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
	_, err := parseMachines("site0-cp-100 10.0.10.100\n"+"site0-cp-101 \n", controlPlaneListing)
	if err == nil || !strings.Contains(err.Error(), "site0-cp-101") {
		t.Fatalf("got %v, want an error naming the machine", err)
	}
}

func TestAClusterThatListsNoMachinesIsAnError(t *testing.T) {
	if _, err := parseMachines("\n", ""); err == nil {
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

func namesOf(ms []machine) string {
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
	if got := namesOf(leaving(fiveAndTwo(), wanted)); got != "site0-cp-103 site0-cp-104" {
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
		t.Fatalf("leaving: %q", namesOf(got))
	}
}

// Workers go first: retiring one cannot move quorum, so the control plane is
// whole while work is being moved.
func TestWorkersLeaveBeforeControlPlanes(t *testing.T) {
	wanted := []string{"10.0.10.100", "10.0.10.101", "10.0.10.102", "10.0.10.200"}
	if got := namesOf(leaving(fiveAndTwo(), wanted)); got != "site0-worker-201 site0-cp-103 site0-cp-104" {
		t.Fatalf("leaving: %q", got)
	}
}

func TestTheJobsOwnMachineIsHandedOverAndTheRestAreRetiredNow(t *testing.T) {
	out := []machine{{Name: "site0-worker-200"}, {Name: "site0-worker-201"}, {Name: "site0-cp-104", ControlPlane: true}}

	now, handed := exceptOwn(out, "site0-worker-201")
	if namesOf(now) != "site0-worker-200 site0-cp-104" {
		t.Errorf("retired now: %q", namesOf(now))
	}
	if handed == nil || handed.Name != "site0-worker-201" {
		t.Errorf("handed over: %+v", handed)
	}
}

func TestNothingIsHandedOverWhenTheJobsMachineIsStaying(t *testing.T) {
	out := []machine{{Name: "site0-cp-103", ControlPlane: true}, {Name: "site0-cp-104", ControlPlane: true}}
	for _, own := range []string{"site0-worker-200", ""} {
		now, handed := exceptOwn(out, own)
		if namesOf(now) != "site0-cp-103 site0-cp-104" || handed != nil {
			t.Errorf("own %q: retired now %q, handed over %+v", own, namesOf(now), handed)
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

// --- the phase, against programs that record what they were asked ----------

type retireFixture struct {
	r   *retirement
	dir string
}

// listingOf is the two answers kubectl gives about these machines.
func listingOf(ms []machine) (lines, controlPlanes string) {
	var cps []string
	for _, m := range ms {
		lines += m.Name + " " + m.IP + "\n"
		if m.ControlPlane {
			cps = append(cps, m.Name)
		}
	}
	return lines, strings.Join(cps, " ")
}

const (
	threeInstances = `{"items": [{"metadata": {"namespace": "database", "name": "tofu-state"},
  "spec": {"instances": 3}, "status": {"readyInstances": 3}}]}`
	noVolumes = `{"items": []}`
)

// newRetireFixture is a cluster of five control planes and two workers, and a
// config that asks for the machines at the addresses given.
func newRetireFixture(t *testing.T, wanted ...string) *retireFixture {
	t.Helper()
	f := &retireFixture{dir: t.TempDir()}
	ctx := run.NewContext(t.TempDir(), "site0")
	if err := os.MkdirAll(ctx.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	net := &config.SiteNetwork{}
	for _, ip := range wanted {
		if strings.HasPrefix(ip, "10.0.10.1") {
			net.NodeIPs = append(net.NodeIPs, ip)
		} else {
			net.WorkerIPs = append(net.WorkerIPs, ip)
		}
	}
	f.r = &retirement{ctx: ctx, kubeconfig: "a-kubeconfig", talosconfig: "a-talosconfig", net: net}

	lines, controlPlanes := listingOf(fiveAndTwo())
	for name, body := range map[string]string{
		"nodes": lines, "control_planes": controlPlanes, "dbs": threeInstances, "pvs": noVolumes, "database_claims": "",
		"members": "5", "drain_exit": "0", "pods": onePodRunning,
	} {
		f.set(t, name, body)
	}
	in := func(name string) string { return filepath.Join(f.dir, name) }
	scripts := map[string]string{
		"tofu": "printf 'a kubeconfig'\n",
		"kubectl": `echo "kubectl $*" >> ` + in("calls") + `
case "$1" in
  get)
    case "$2" in
      nodes) case "$3" in -l) cat ` + in("control_planes") + ` ;; *) cat ` + in("nodes") + ` ;; esac ;;
      clusters.postgresql.cnpg.io) cat ` + in("dbs") + ` ;;
      persistentvolumes) cat ` + in("pvs") + ` ;;
      persistentvolumeclaims) cat ` + in("database_claims") + ` ;;
      pods) cat ` + in("pods") + ` ;;
    esac ;;
  drain) exit "$(cat ` + in("drain_exit") + `)" ;;
esac
`,
		// etcd has as many members as the file says, and a control plane
		// that is reset leaves it.
		"talosctl": `echo "talosctl $*" >> ` + in("calls") + `
case "$*" in
  *"etcd members"*)
    echo "NODE           ID                   HOSTNAME       PEER URLS                   CLIENT URLS                 LEARNER"
    i=0
    while [ "$i" -lt "$(cat ` + in("members") + `)" ]; do
      echo "192.0.2.10$i    aaaaaaaaaaaaaaaa     site0-cp-10$i   https://192.0.2.10$i:2380    https://192.0.2.10$i:2379    false"
      i=$((i+1))
    done ;;
  *reset*)
    case "$2" in 10.0.10.1*) echo $(( $(cat ` + in("members") + `) - 1 )) > ` + in("members") + ` ;; esac ;;
esac
`,
	}
	for name, body := range scripts {
		if err := os.WriteFile(in(name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", f.dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return f
}

func (f *retireFixture) set(t *testing.T, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *retireFixture) calls() string {
	b, _ := os.ReadFile(filepath.Join(f.dir, "calls"))
	return string(b)
}

// run is the phase from the point it has its credentials.
func (f *retireFixture) run(t *testing.T, own string) error {
	t.Helper()
	out, err := f.r.leaving()
	if err != nil {
		t.Fatal(err)
	}
	return f.r.retire(out, own)
}

// inOrder fails unless every line appears in the calls, each after the last.
func inOrder(t *testing.T, calls string, want ...string) {
	t.Helper()
	at := 0
	for _, w := range want {
		i := strings.Index(calls[at:], w)
		if i < 0 {
			t.Fatalf("%q was not asked for, or not after what should come before it:\n%s", w, calls)
		}
		at += i + len(w)
	}
}

const (
	windowOpen   = `"inProgress":true,"reusePVC":false`
	windowClosed = `"inProgress":false`
	threeAndTwo  = "10.0.10.100 10.0.10.101 10.0.10.102 10.0.10.200 10.0.10.201"
)

// The epoch's acceptance test: five control planes, a config that asks for
// three, and two machines taken out one at a time, each emptied, reset and
// deleted before the next is touched.
func TestFiveControlPlanesBecomeThreeOneAtATime(t *testing.T) {
	f := newRetireFixture(t, strings.Fields(threeAndTwo)...)
	if err := f.run(t, ""); err != nil {
		t.Fatal(err)
	}
	inOrder(t, f.calls(),
		"talosctl --nodes 10.0.10.100 etcd members",
		windowOpen,
		"kubectl drain site0-cp-103 --ignore-daemonsets",
		"talosctl --nodes 10.0.10.103 reset --graceful=true",
		"kubectl delete node site0-cp-103",
		"talosctl --nodes 10.0.10.100 etcd members",
		windowClosed,
		windowOpen,
		"kubectl drain site0-cp-104",
		"talosctl --nodes 10.0.10.104 reset --graceful=true",
		"kubectl delete node site0-cp-104",
		windowClosed,
	)
	for _, staying := range []string{"site0-cp-100", "site0-cp-102", "site0-worker-200", "10.0.10.100 reset"} {
		if strings.Contains(f.calls(), "drain "+staying) || strings.Contains(f.calls(), "--nodes "+staying) {
			t.Errorf("%s is staying and was disturbed:\n%s", staying, f.calls())
		}
	}
}

func TestTheJobsOwnMachineIsCordonedAndNotRetired(t *testing.T) {
	f := newRetireFixture(t, "10.0.10.100", "10.0.10.101", "10.0.10.102", "10.0.10.103", "10.0.10.200")
	err := f.run(t, "site0-worker-201")

	var handed *HandedOver
	if !errors.As(err, &handed) || handed.Machine != "site0-worker-201" {
		t.Fatalf("got %v, want the machine handed over", err)
	}
	inOrder(t, f.calls(), "kubectl delete node site0-cp-104", "kubectl cordon site0-worker-201")
	for _, never := range []string{"drain site0-worker-201", "--nodes 10.0.10.201", "delete node site0-worker-201"} {
		if strings.Contains(f.calls(), never) {
			t.Errorf("the job's own machine was retired by the job on it (%q):\n%s", never, f.calls())
		}
	}
}

func disturbed(calls string) bool {
	for _, verb := range []string{"kubectl drain", "kubectl patch", "kubectl delete", "kubectl cordon", " reset "} {
		if strings.Contains(calls, verb) {
			return true
		}
	}
	return false
}

func TestADatabaseWithOneInstanceRefusesBeforeAnythingIsTouched(t *testing.T) {
	f := newRetireFixture(t, strings.Fields(threeAndTwo)...)
	f.set(t, "dbs", `{"items": [{"metadata": {"namespace": "thing", "name": "only"}, "spec": {"instances": 1}, "status": {"readyInstances": 1}}]}`)
	err := f.run(t, "")
	if err == nil || !strings.Contains(err.Error(), "thing/only") {
		t.Fatalf("got %v, want a refusal naming the database", err)
	}
	if disturbed(f.calls()) {
		t.Errorf("something was changed before the refusal:\n%s", f.calls())
	}
}

const (
	aVolumeOn104 = `{"items": [
  {"spec": {"claimRef": {"namespace": "games", "name": "world"},
    "nodeAffinity": {"required": {"nodeSelectorTerms": [{"matchExpressions": [{"key": "kubernetes.io/hostname", "values": ["site0-cp-104"]}]}]}}}},
  {"spec": {"claimRef": {"namespace": "database", "name": "tofu-state-2"},
    "nodeAffinity": {"required": {"nodeSelectorTerms": [{"matchExpressions": [{"key": "kubernetes.io/hostname", "values": ["site0-cp-104"]}]}]}}}},
  {"spec": {"claimRef": {"namespace": "games", "name": "elsewhere"},
    "nodeAffinity": {"required": {"nodeSelectorTerms": [{"matchExpressions": [{"key": "kubernetes.io/hostname", "values": ["site0-cp-100"]}]}]}}}}]}`
	// The claims a database owns, as kubectl lists them by its label.
	databaseClaims = "database/tofu-state-2\n"
)

// A database's volume is its operator's to rebuild, and a volume on a machine
// that is staying is nobody's concern. What is left is lost with the machine.
func TestAVolumeNothingWillMoveKeepsItsMachine(t *testing.T) {
	f := newRetireFixture(t, strings.Fields(threeAndTwo)...)
	f.set(t, "pvs", aVolumeOn104)
	f.set(t, "database_claims", databaseClaims)
	err := f.run(t, "")
	if err == nil || !strings.Contains(err.Error(), "games/world") {
		t.Fatalf("got %v, want a refusal naming the claim", err)
	}
	for _, not := range []string{"tofu-state-2", "games/elsewhere"} {
		if strings.Contains(err.Error(), not) {
			t.Errorf("%s was named, and it is not stranded: %v", not, err)
		}
	}
	if disturbed(f.calls()) {
		t.Errorf("something was changed before the refusal:\n%s", f.calls())
	}
}

func TestAMachineThatWillNotEmptyIsNotReset(t *testing.T) {
	f := newRetireFixture(t, strings.Fields(threeAndTwo)...)
	f.set(t, "drain_exit", "1")
	err := f.run(t, "")
	if err == nil || !strings.Contains(err.Error(), "site0-cp-103") {
		t.Fatalf("got %v, want the machine that would not empty", err)
	}
	if strings.Contains(f.calls(), " reset ") || strings.Contains(f.calls(), "delete node") {
		t.Errorf("a machine with work still on it was reset or deleted:\n%s", f.calls())
	}
	// Left open, the window stops the database healing itself.
	inOrder(t, f.calls(), "kubectl drain site0-cp-103", windowClosed)
}

func TestAControlPlaneStaysWhileEtcdIsNotSound(t *testing.T) {
	f := newRetireFixture(t, strings.Fields(threeAndTwo)...)
	f.set(t, "members", "4") // five control planes, and one is not a member
	err := f.run(t, "")
	if err == nil || !strings.Contains(err.Error(), "etcd") {
		t.Fatalf("got %v, want a refusal about etcd", err)
	}
	if disturbed(f.calls()) {
		t.Errorf("a control plane was disturbed with etcd unsound:\n%s", f.calls())
	}
}

func TestEveryDeclaredWorkloadIsBackedUpBeforeAMachineIsDrained(t *testing.T) {
	f := newRetireFixture(t, strings.Fields(threeAndTwo)...)
	path := filepath.Join(f.r.ctx.RepoRoot, filepath.FromSlash(applications.Dir), "thing", applications.Declaration)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(aDeclaration), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.run(t, ""); err != nil {
		t.Fatal(err)
	}
	inOrder(t, f.calls(), "-c saver -- /bin/save --now", "kubectl drain site0-cp-103")
}
