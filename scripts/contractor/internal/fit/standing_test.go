package fit

import (
	"strings"
	"testing"
)

// Three control planes and three workers, as `kubectl get nodes -o json`
// lists them. The workers hold a little under ten gibibytes each for pods,
// and one is larger than the others.
const nodes = `{"items":[
 {"metadata":{"name":"cp-0","labels":{"node-role.kubernetes.io/control-plane":""}},"status":{"allocatable":{"memory":"3800000Ki"}}},
 {"metadata":{"name":"cp-1","labels":{"node-role.kubernetes.io/control-plane":""}},"status":{"allocatable":{"memory":"3800000Ki"}}},
 {"metadata":{"name":"wk-0","labels":{}},"status":{"allocatable":{"memory":"9663676416"}}},
 {"metadata":{"name":"wk-1","labels":{}},"status":{"allocatable":{"memory":"9437184Ki"}}},
 {"metadata":{"name":"wk-2","labels":{}},"status":{"allocatable":{"memory":"12Gi"}}}
]}`

func pod(name, node, phase, priority string, requests ...string) string {
	var containers []string
	for _, r := range requests {
		containers = append(containers, `{"resources":{"requests":{"memory":"`+r+`"}}}`)
	}
	return `{"metadata":{"name":"` + name + `"},"spec":{"nodeName":"` + node + `","priorityClassName":"` + priority +
		`","containers":[` + strings.Join(containers, ",") + `]},"status":{"phase":"` + phase + `"}}`
}

func pods(items ...string) []byte { return []byte(`{"items":[` + strings.Join(items, ",") + `]}`) }

// What the workers hold is the workers', in whole gibibytes: rounded down,
// because a part of a gibibyte is not room to promise. The largest is
// rounded up, because it is what is taken away.
func TestWhatTheWorkersHoldIsCountedInWholeGibibytesAndNotTheControlPlanes(t *testing.T) {
	site, err := Standing([]byte(nodes), pods())
	if err != nil {
		t.Fatal(err)
	}
	// 9 + 9 + 12 gibibytes, to the gibibyte below each.
	if site.Workers != 30*gib {
		t.Errorf("the workers hold %d GiB, and three of nine, nine and twelve hold thirty", site.Workers/gib)
	}
	if site.LargestWorker != 12*gib {
		t.Errorf("the largest worker holds %d GiB, not twelve", site.LargestWorker/gib)
	}
}

// What cannot wait is every running pod on a worker that is not batch, by
// what it reserves, rounded up to the gibibyte above the total.
func TestWhatCannotWaitIsEveryRunningPodOnAWorkerThatIsNotBatch(t *testing.T) {
	site, err := Standing([]byte(nodes), pods(
		pod("a", "wk-0", "Running", "interactive", "2Gi", "256Mi"),
		pod("b", "wk-1", "Running", "critical", "1500Mi"),
		pod("c", "wk-2", "Running", "", "512Mi"),
		// Not counted: work that can wait, a pod on a control plane, and
		// pods that are finished or not yet anywhere.
		pod("d", "wk-0", "Running", "batch", "4Gi"),
		pod("e", "cp-0", "Running", "critical", "1Gi"),
		pod("f", "wk-1", "Succeeded", "interactive", "1Gi"),
		pod("g", "", "Pending", "interactive", "1Gi"),
	))
	if err != nil {
		t.Fatal(err)
	}
	// 2048 + 256 + 1500 + 512 = 4316 MiB, which is a little over four.
	if site.MustRun != 5*gib {
		t.Errorf("work that cannot wait reserves %d GiB, and 4316 MiB is five to the gibibyte above", site.MustRun/gib)
	}
}

// A listing with no worker in it is not a site with nothing to hold: it is
// a listing that was not read, and saying zero would make every change fit
// nothing or everything by accident.
func TestAListingWithNoWorkerIsAnErrorAndNotASiteOfNothing(t *testing.T) {
	onlyControlPlanes := `{"items":[{"metadata":{"name":"cp-0","labels":{"node-role.kubernetes.io/control-plane":""}},"status":{"allocatable":{"memory":"4Gi"}}}]}`
	for name, listing := range map[string]string{"no machines": `{"items":[]}`, "no workers": onlyControlPlanes} {
		if _, err := Standing([]byte(listing), pods()); err == nil {
			t.Errorf("a listing with %s was read as a site", name)
		}
	}
}

// A size this cannot read is refused by name, never counted as nothing.
func TestASizeThatCannotBeReadIsRefused(t *testing.T) {
	for _, written := range []string{"", "lots", "12Qi", "-1Gi", "1.5.2Gi"} {
		if _, err := Bytes(written); err == nil {
			t.Errorf("%q was read as a size", written)
		}
	}
	for written, want := range map[string]int64{
		"9663676416": 9663676416, "9437184Ki": 9437184 << 10, "512Mi": 512 << 20, "2Gi": 2 << 30,
		"1G": 1_000_000_000, "500M": 500_000_000, "128974848": 128974848, "1.5Gi": 3 << 29, "129e6": 129_000_000,
	} {
		got, err := Bytes(written)
		if err != nil || got != want {
			t.Errorf("%q was read as %d (%v), not %d", written, got, err, want)
		}
	}
	bad := pods(pod("a", "wk-0", "Running", "interactive", "lots"))
	if _, err := Standing([]byte(nodes), bad); err == nil || !strings.Contains(err.Error(), "a") {
		t.Errorf("a pod reserving a size that cannot be read was counted, or not named: %v", err)
	}
}

// What is kept comes back as it was written, and is written in gibibytes a
// person can read.
func TestAStandingIsKeptInWholeGibibytesAndComesBackTheSame(t *testing.T) {
	site := Site{Workers: 27 * gib, LargestWorker: 10 * gib, MustRun: 11 * gib}
	raw, err := Marshal(site, "2026-10-09T04:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	for _, said := range []string{`"workers_gib": 27`, `"largest_worker_gib": 10`, `"must_run_gib": 11`} {
		if !strings.Contains(string(raw), said) {
			t.Errorf("the file does not say %s:\n%s", said, raw)
		}
	}
	back, taken, err := Unmarshal(raw)
	if err != nil || back != site || taken != "2026-10-09T04:00:00Z" {
		t.Errorf("came back as %+v taken %q (%v)", back, taken, err)
	}
	if _, _, err := Unmarshal([]byte("not json")); err == nil {
		t.Error("something that is not a standing was read as one")
	}
}
