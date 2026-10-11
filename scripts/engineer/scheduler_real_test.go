package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"homelab/details/capacity"
)

// needsScheduler is set where the scheduler's programs have been delivered,
// which is everywhere this test is meant to prove something. Without it a
// machine that has none of them skips; with it, a missing program is a
// failure and not a quiet pass.
const needsScheduler = "ENGINEER_NEEDS_SCHEDULER"

// The whole of it, with nothing standing in: a site of two workers is
// loaded into a real API server and judged by a real scheduler.
//
// The site is the case a sum cannot see. Each worker has two gibibytes
// free, so four are free between them, and a pod of three fits the total
// and fits no machine.
func TestTheRealSchedulerRefusesWhatFitsTheTotalAndNoMachine(t *testing.T) {
	for _, program := range []string{starter, asker, store, apiServer, scheduler} {
		if _, err := exec.LookPath(program); err != nil {
			if os.Getenv(needsScheduler) == "" {
				t.Skipf("%s is not on this machine, so the real scheduler is not tried here", program)
			}
			t.Fatalf("%s was to have been delivered and is not on the path: %v", program, err)
		}
	}
	s, err := capacity.Take(capacity.Listings{
		Nodes: []byte(`{"items":[
 {"metadata":{"name":"s-cp-100","labels":{"node-role.kubernetes.io/control-plane":""}},"spec":{"taints":[{"key":"node-role.kubernetes.io/control-plane","effect":"NoSchedule"}]},"status":{"allocatable":{"memory":"4Gi","cpu":"2","pods":"110"}}},
 {"metadata":{"name":"s-wk-200","labels":{"kubernetes.io/hostname":"s-wk-200"}},"status":{"allocatable":{"memory":"8Gi","cpu":"4","pods":"110"}}},
 {"metadata":{"name":"s-wk-201","labels":{"kubernetes.io/hostname":"s-wk-201"}},"status":{"allocatable":{"memory":"8Gi","cpu":"4","pods":"110"}}}]}`),
		Namespaces:      []byte(`{"items":[{"metadata":{"name":"running"}}]}`),
		PriorityClasses: []byte(`{"items":[{"metadata":{"name":"interactive"},"value":10000,"preemptionPolicy":"Never"},{"metadata":{"name":"critical"},"value":1000000}]}`),
		StorageClasses:  []byte(`{"items":[]}`), Volumes: []byte(`{"items":[]}`), Claims: []byte(`{"items":[]}`),
		Pods: []byte(`{"items":[
 {"metadata":{"namespace":"running","name":"a","ownerReferences":[{"kind":"ReplicaSet","name":"a-1"}]},"spec":{"nodeName":"s-wk-200","priorityClassName":"interactive","containers":[{"name":"c","resources":{"requests":{"memory":"6Gi"}}}]},"status":{"phase":"Running"}},
 {"metadata":{"namespace":"running","name":"b","ownerReferences":[{"kind":"ReplicaSet","name":"b-1"}]},"spec":{"nodeName":"s-wk-201","priorityClassName":"interactive","containers":[{"name":"c","resources":{"requests":{"memory":"6Gi"}}}]},"status":{"phase":"Running"}}]}`),
	}, nil, "now")
	if err != nil {
		t.Fatal(err)
	}
	three := asks{Core: map[string]workload{"a setting": {Pods: 1, PodBytes: 3 << 30}}}
	one := asks{Core: map[string]workload{"a setting": {Pods: 1, PodBytes: 1 << 30}}}

	fits, err := realScheduler(scenario(s, "", asks{}, one))
	if err != nil {
		t.Fatal(err)
	}
	if len(fits) != 0 {
		t.Errorf("a pod of one gibibyte, with two free on each worker, was left waiting: %v", fits)
	}
	refused, err := realScheduler(scenario(s, "", asks{}, three))
	if err != nil {
		t.Fatal(err)
	}
	if len(refused) != 1 {
		t.Fatalf("a pod of three gibibytes, with two free on each worker, left %d waiting, not itself alone: %v", len(refused), refused)
	}
	for _, why := range refused {
		if !strings.Contains(why, "Insufficient memory") {
			t.Errorf("it was refused for something other than memory: %s", why)
		}
	}
}
