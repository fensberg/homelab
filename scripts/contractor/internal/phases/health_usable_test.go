package phases

import (
	"strings"
	"testing"
)

// "The machines exist and Kubernetes calls them Ready" and "the machines are
// usable" are different claims. A scale-up from three nodes to five proved the
// first - five nodes, all Ready, count matching the config - and nothing in
// this phase asked the second.

// A cordoned node keeps its Ready condition True. Nothing else in this phase
// reads spec.unschedulable, so two cordoned machines would arrive as two more
// healthy nodes that the cluster will never place anything on.
func TestUnschedulableFindsACordonedNode(t *testing.T) {
	const nodes = `{"items":[
      {"metadata":{"name":"site0-cp-100"},"spec":{}},
      {"metadata":{"name":"site0-cp-103"},"spec":{"unschedulable":true}}
    ]}`
	got, err := unschedulable([]byte(nodes))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0], "site0-cp-103") {
		t.Fatalf("got %v, want the cordoned node named", got)
	}
}

func TestUnschedulablePassesAFleetThatIsAllSchedulable(t *testing.T) {
	const nodes = `{"items":[{"metadata":{"name":"site0-cp-100"},"spec":{}}]}`
	got, err := unschedulable([]byte(nodes))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want nothing", got)
	}
}

// The check that answers the scale-up question. Adding a node raises every
// DaemonSet's desiredNumberScheduled, so a machine that cannot run the CNI,
// kube-proxy or the storage provisioner shows up as a shortfall immediately
// rather than as a mystery later.
func TestDaemonSetShortfallsNamesTheOneThatDidNotSpread(t *testing.T) {
	const dss = `{"items":[
      {"metadata":{"name":"kube-proxy","namespace":"kube-system"},
       "status":{"desiredNumberScheduled":5,"numberReady":5}},
      {"metadata":{"name":"openebs-localpv","namespace":"openebs"},
       "status":{"desiredNumberScheduled":5,"numberReady":3}}
    ]}`
	got, err := daemonSetShortfalls([]byte(dss))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0], "openebs/openebs-localpv") || !strings.Contains(got[0], "3 of 5") {
		t.Fatalf("got %v, want openebs-localpv named with 3 of 5", got)
	}
}

// A status with no counts yet is a DaemonSet that has not been shown to run
// anywhere. Reading absent fields as "as many as we wanted" is how a gate
// passes before the thing it gates on has begun.
func TestDaemonSetShortfallsTreatsAnEmptyStatusAsNotReady(t *testing.T) {
	const dss = `{"items":[{"metadata":{"name":"cni","namespace":"kube-system"},
                            "status":{"desiredNumberScheduled":5}}]}`
	got, err := daemonSetShortfalls([]byte(dss))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.Contains(got[0], "0 of 5") {
		t.Fatalf("got %v, want the DaemonSet reported as 0 of 5", got)
	}
}

// No DaemonSets at all is a wrong answer, not a healthy one: this cluster runs
// its CNI and kube-proxy that way. Same reasoning as the Flux check refusing
// an empty list - "nothing is failing" and "nothing exists yet" look identical
// if you only count failures.
func TestDaemonSetShortfallsRefusesAnEmptyList(t *testing.T) {
	if _, err := daemonSetShortfalls([]byte(`{"items":[]}`)); err == nil {
		t.Fatal("an empty DaemonSet list passed; it should be refused as a wrong answer")
	}
}

func TestDaemonSetShortfallsPassesAFullyScheduledCluster(t *testing.T) {
	const dss = `{"items":[{"metadata":{"name":"kube-proxy","namespace":"kube-system"},
                            "status":{"desiredNumberScheduled":5,"numberReady":5}}]}`
	got, err := daemonSetShortfalls([]byte(dss))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %v, want nothing", got)
	}
}

// --- the verdict, not just its parts ----------------------------------------
//
// These exist because testing the parsers alone was not enough. The cordon
// check could be deleted from checkNodes and every test above still passed,
// which is the same defect shape as a push-guard test that passed through an
// unrelated error path: a suite that would not notice the behaviour it names
// being removed outright.

const threeReadyNodes = `{"items":[
  {"metadata":{"name":"site0-cp-100"},"spec":{},"status":{"conditions":[{"type":"Ready","status":"True"}]}},
  {"metadata":{"name":"site0-cp-101"},"spec":{},"status":{"conditions":[{"type":"Ready","status":"True"}]}},
  {"metadata":{"name":"site0-cp-102"},"spec":{},"status":{"conditions":[{"type":"Ready","status":"True"}]}}
]}`

func TestNodeVerdictPassesAHealthyFleetOfTheRightSize(t *testing.T) {
	if err := nodeVerdict([]byte(threeReadyNodes), 3); err != nil {
		t.Fatalf("a healthy three-node cluster asked for three was refused: %v", err)
	}
}

// The scale-up case. Three Ready nodes against a config asking for five is
// every node healthy and two missing, and "no node is unready" is true of both.
func TestNodeVerdictCatchesNodesThatNeverJoined(t *testing.T) {
	err := nodeVerdict([]byte(threeReadyNodes), 5)
	if err == nil || !strings.Contains(err.Error(), "expected 5") {
		t.Fatalf("got %v, want a refusal naming the expected count", err)
	}
}

func TestNodeVerdictCatchesAnUnreadyNode(t *testing.T) {
	const nodes = `{"items":[
      {"metadata":{"name":"site0-cp-100"},"spec":{},"status":{"conditions":[{"type":"Ready","status":"False","message":"kubelet is not posting ready status"}]}}
    ]}`
	err := nodeVerdict([]byte(nodes), 1)
	if err == nil || !strings.Contains(err.Error(), "not Ready") {
		t.Fatalf("got %v, want a refusal naming the unready node", err)
	}
}

// The one the parser tests could not protect. A cordoned node is Ready, is
// counted, and is useless - so this must be refused by the verdict itself, not
// merely detectable by a helper nothing is obliged to call.
func TestNodeVerdictRefusesACordonedNodeEvenWhenReadyAndCounted(t *testing.T) {
	const nodes = `{"items":[
      {"metadata":{"name":"site0-cp-100"},"spec":{},"status":{"conditions":[{"type":"Ready","status":"True"}]}},
      {"metadata":{"name":"site0-cp-103"},"spec":{"unschedulable":true},"status":{"conditions":[{"type":"Ready","status":"True"}]}}
    ]}`
	err := nodeVerdict([]byte(nodes), 2)
	if err == nil {
		t.Fatal("a cordoned node passed: it is Ready and counted, which is exactly why the Ready condition and the count are not enough")
	}
	if !strings.Contains(err.Error(), "site0-cp-103") {
		t.Fatalf("got %v, want the cordoned node named", err)
	}
}

// The phase must actually ask all four questions. Every check shells out to
// kubectl, so the phase cannot be exercised here - but the list it runs can,
// and a deleted entry is otherwise three green lines and a smaller file.
func TestHealthAsksEveryQuestionItClaimsTo(t *testing.T) {
	want := []string{"nodes", "Flux reconciliation", "the state database", "per-node workloads"}
	if len(healthChecks) != len(want) {
		t.Fatalf("the phase runs %d checks, want %d: %v", len(healthChecks), len(want), want)
	}
	for i, w := range want {
		if healthChecks[i].name != w {
			t.Errorf("check %d is %q, want %q", i, healthChecks[i].name, w)
		}
		if healthChecks[i].check == nil {
			t.Errorf("check %q has no function", w)
		}
		if healthChecks[i].timeout <= 0 {
			t.Errorf("check %q has no timeout, so it would spin forever", w)
		}
	}
}
