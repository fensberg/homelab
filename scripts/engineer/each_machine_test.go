package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"homelab/details/capacity"
)

// onEvery is a workload that runs a pod on every machine, as a tree asks for
// it: what each pod reserves, the class it has and the rule it goes by.
func onEvery(mebibytes int64) asks {
	return asks{
		Core: map[string]workload{"DaemonSet/net/agent": {Pods: 2, PodBytes: mebibytes << 20}},
		Each: map[string]capacity.Object{"DaemonSet/net/agent": {
			capacity.PriorityClassKey: "system-node-critical",
			capacity.NodeSelectorKey:  map[string]any{"runs": "it", capacity.OSLabel: "linux"},
			"affinity": map[string]any{
				"podAffinity":            map[string]any{"near": "another pod"},
				capacity.NodeAffinityKey: map[string]any{"only": "some machines"},
			},
			capacity.SpreadKey: []any{map[string]any{"spread": "out"}},
			"tolerations":      []any{map[string]any{"operator": "Exists"}},
			"containers":       []any{map[string]any{"name": "c0"}},
		}},
	}
}

// The network is part of what a site is asked to run, and a workload that
// runs on every machine is kept with where its pods may go.
func TestTheNetworkIsReadAndAWorkloadOnEveryMachineKeepsItsRules(t *testing.T) {
	dir := tree(t, map[string][]string{
		"clusters/bootstrap/network.yaml": {
			`{"kind":"DaemonSet","metadata":{"name":"agent","namespace":"net"},"spec":{"template":{"spec":{"priorityClassName":"system-node-critical",
			  "tolerations":[{"operator":"Exists"}],"containers":[{"name":"a","image":"images.invalid/a:1","resources":{"requests":{"memory":"256Mi"}}}]}}}}`,
		},
		"clusters/bootstrap/network-values.yaml": {`{"resources":{"requests":{"memory":"256Mi"}}}`},
		"clusters/core/a.yaml":                   {deployment("thing", "1", "", "1Gi")},
	})
	got, err := asked(dir, "north", 3, asDrawn)
	if err != nil {
		t.Fatal(err)
	}
	if w := got.Core["DaemonSet/net/agent"]; w.PodBytes != 256<<20 || w.Pods != 3 {
		t.Fatalf("the network's agent is asked for as %+v, not a pod of 256 MiB on each of three workers", w)
	}
	raw, _ := json.Marshal(got.Each["DaemonSet/net/agent"])
	for _, held := range []string{`"priorityClassName":"system-node-critical"`, `"operator":"Exists"`} {
		if !strings.Contains(string(raw), held) {
			t.Errorf("the agent is kept without %s: %s", held, raw)
		}
	}
	if len(got.Each) != 1 || len(got.Core) != 2 {
		t.Errorf("a file of settings was read as a workload, or a workload that is not on every machine was kept as one: %v %v", got.Core, got.Each)
	}
}

// What a workload on every machine is raised by is tried on each machine it
// could be on: a pod of that much, held to that machine, with the workload's
// own class and rules. It is not also tried as pods that may go anywhere.
func TestWhatIsRaisedOnEveryMachineIsTriedOnEachMachine(t *testing.T) {
	tr := scenario(north(t), "wk-201", onEvery(64), onEvery(448))
	var on []string
	for _, o := range tr.Objects {
		name := dig(o, "metadata", "name")
		if o["kind"] != capacity.KindPod || dig(o, "metadata", "namespace") != more {
			continue
		}
		raw, _ := json.Marshal(o["spec"])
		spec := string(raw)
		machine := dig(o, "spec", capacity.NodeSelectorKey, capacity.HostnameLabel)
		on = append(on, machine.(string))
		for _, held := range []string{`"memory":"402653184"`, `"priorityClassName":"system-node-critical"`, `"runs":"it"`, `"operator":"Exists"`} {
			if !strings.Contains(spec, held) {
				t.Errorf("%v does not hold %s: %s", name, held, spec)
			}
		}
		// Its rules about machines, and none about other pods or about
		// what kind of machine a machine is.
		for _, dropped := range []string{"podAffinity", capacity.SpreadKey, capacity.OSLabel} {
			if strings.Contains(spec, dropped) {
				t.Errorf("%v is held to %s, which a trial cannot follow: %s", name, dropped, spec)
			}
		}
		if !strings.Contains(spec, `"only":"some machines"`) {
			t.Errorf("%v lost the workload's rule about machines: %s", name, spec)
		}
		if dig(o, "spec", "nodeName") != nil {
			t.Errorf("%v is put on its machine and not left to the scheduler, so nothing is asked of whether it fits", name)
		}
		if called := tr.Called[more+"/"+name.(string)]; !strings.Contains(called, "DaemonSet/net/agent") || !strings.Contains(called, "384 MiB") || !strings.Contains(called, machine.(string)) {
			t.Errorf("%v is called %q", name, called)
		}
	}
	// The control plane and the worker that is left, and not the one gone.
	if strings.Join(on, " ") != "cp-100 wk-200" {
		t.Errorf("what was raised is tried on %v, not on each machine that is there", on)
	}
	if lowered := scenario(north(t), "", onEvery(448), onEvery(64)); len(lowered.Called) != len(scenario(north(t), "", onEvery(64), onEvery(64)).Called) {
		t.Error("a workload that was lowered was tried as if it asked for more")
	}
}

// A machine the workload does not run on leaves its pod waiting whatever is
// asked. The site before the change is tried with the same pods asking for
// nothing, so that such a pod is not held against the change.
func TestAMachineTheWorkloadIsNotOnIsNotHeldAgainstTheChange(t *testing.T) {
	asksNothing := 0
	// A scheduler that never places the pod held to the control plane, and
	// places the one held to a worker only while it asks for nothing.
	place := func(tr trial) (map[string]string, error) {
		waiting := map[string]string{}
		for _, o := range tr.Objects {
			if o["kind"] != capacity.KindPod || dig(o, "metadata", "namespace") != more {
				continue
			}
			id := more + "/" + dig(o, "metadata", "name").(string)
			raw, _ := json.Marshal(o["spec"])
			nothing := strings.Contains(string(raw), `"memory":"0"`)
			if nothing {
				asksNothing++
			}
			if strings.HasSuffix(id, "cp-100") {
				waiting[id] = "0/3 nodes are available: 3 node(s) didn't match Pod's node affinity/selector."
			} else if !nothing {
				waiting[id] = "0/3 nodes are available: 1 Insufficient memory."
			}
		}
		return waiting, nil
	}
	held, err := held(north(t), onEvery(64), onEvery(448), place)
	if err != nil {
		t.Fatal(err)
	}
	said := strings.Join(held, "\n")
	if asksNothing == 0 {
		t.Fatal("the site before the change was not tried with the pods asking for nothing")
	}
	if strings.Contains(said, "cp-100") {
		t.Errorf("a machine the workload is not on was held against the change:\n%s", said)
	}
	for _, want := range []string{"on wk-200, 384 MiB: 0/3 nodes are available: 1 Insufficient memory", "on wk-201, 384 MiB"} {
		if !strings.Contains(said, want) {
			t.Errorf("a worker left short by the change was not named with %q:\n%s", want, said)
		}
	}
}

// What happened, with nothing standing in. A worker holds an application
// its volume keeps there, with a few hundred mebibytes left over. The
// network's agent, which outranks it, is given a reservation on every
// machine that is more than what is left. The total fits easily: the other
// worker is nearly empty. The scheduler puts the application off its
// machine for the agent, and it has no other machine to go to.
func TestTheRealSchedulerRefusesAReservationOnEveryMachineThatCostsOneItsApplication(t *testing.T) {
	for _, program := range []string{starter, asker, store, apiServer, scheduler} {
		if _, err := exec.LookPath(program); err != nil {
			if os.Getenv(needsScheduler) == "" {
				t.Skipf("%s is not on this machine, so the real scheduler is not tried here", program)
			}
			t.Fatalf("%s was to have been delivered and is not on the path: %v", program, err)
		}
	}
	machine := func(name, labels string) string {
		return `{"metadata":{"name":"` + name + `","labels":{"kubernetes.io/hostname":"` + name + `"` + labels + `}},"status":{"allocatable":{"memory":"9Gi","cpu":"8","pods":"110"}}}`
	}
	s, err := capacity.Take(capacity.Listings{
		Nodes:      []byte(`{"items":[` + machine("example-cp-100", `,"node-role.kubernetes.io/control-plane":""`) + `,` + machine("example-wk-200", "") + `,` + machine("example-wk-201", "") + `]}`),
		Namespaces: []byte(`{"items":[{"metadata":{"name":"pastime"}}]}`),
		PriorityClasses: []byte(`{"items":[{"metadata":{"name":"interactive"},"value":10000,"preemptionPolicy":"Never"},
		  {"metadata":{"name":"system-node-critical"},"value":2000001000}]}`),
		StorageClasses: []byte(capacity.Nothing),
		Volumes: []byte(`{"items":[{"metadata":{"name":"v"},"spec":{"capacity":{"storage":"1Gi"},"accessModes":["ReadWriteOnce"],"storageClassName":"kept",
		  "claimRef":{"namespace":"pastime","name":"saves"},
		  "nodeAffinity":{"required":{"nodeSelectorTerms":[{"matchExpressions":[{"key":"kubernetes.io/hostname","operator":"In","values":["example-wk-200"]}]}]}}}}]}`),
		Claims: []byte(`{"items":[{"metadata":{"namespace":"pastime","name":"saves"},"spec":{"accessModes":["ReadWriteOnce"],"storageClassName":"kept","volumeName":"v","resources":{"requests":{"storage":"1Gi"}}}}]}`),
		Pods: []byte(`{"items":[{"metadata":{"namespace":"pastime","name":"world-1-a","ownerReferences":[{"kind":"ReplicaSet","name":"world-1"}]},
		  "spec":{"nodeName":"example-wk-200","priorityClassName":"interactive","containers":[{"name":"c","resources":{"requests":{"memory":"8704Mi"}}}],
		  "volumes":[{"name":"s","persistentVolumeClaim":{"claimName":"saves"}}]},"status":{"phase":"Running"}}]}`),
	}, nil, "now")
	if err != nil {
		t.Fatal(err)
	}
	agent := func(mebibytes int64) asks {
		return asks{
			Core: map[string]workload{"DaemonSet/net/agent": {Pods: 2, PodBytes: mebibytes << 20}},
			Each: map[string]capacity.Object{"DaemonSet/net/agent": {capacity.PriorityClassKey: "system-node-critical"}},
		}
	}
	// 512 MiB are left on the application's worker.
	if waits, err := held(s, agent(0), agent(256), realScheduler); err != nil || len(waits) != 0 {
		t.Errorf("an agent of 256 MiB on every machine, with 512 left on the fullest, was refused: %v %v", waits, err)
	}
	waits, err := held(s, agent(0), agent(640), realScheduler)
	if err != nil {
		t.Fatal(err)
	}
	said := strings.Join(waits, "\n")
	if !strings.Contains(said, "With every machine running") || !strings.Contains(said, "a pod the site runs today, made by a ReplicaSet, reserving 8704Mi") {
		t.Errorf("an agent of 640 MiB on every machine, with 512 left on the application's only worker, did not cost the application its machine:\n%s", said)
	}
}

// A workload that could be placed on no machine at all, even asking for
// nothing, is a rule the trial could not follow. It is an error, and never
// every machine quietly left out.
func TestAWorkloadATrialCanPlaceNowhereIsAnErrorAndNotAPass(t *testing.T) {
	nowhere := func(tr trial) (map[string]string, error) {
		waiting := map[string]string{}
		for _, o := range tr.Objects {
			if o["kind"] == capacity.KindPod && dig(o, "metadata", "namespace") == more {
				waiting[more+"/"+dig(o, "metadata", "name").(string)] = "0/3 nodes are available: 3 node(s) didn't match Pod's node affinity/selector."
			}
		}
		return waiting, nil
	}
	_, err := held(north(t), onEvery(64), onEvery(448), nowhere)
	if err == nil || !strings.Contains(err.Error(), "DaemonSet/net/agent could be placed on no machine at all") || !strings.Contains(err.Error(), "didn't match") {
		t.Errorf("a change to a workload the trial could place nowhere was passed, or not said to be untried: %v", err)
	}
}
