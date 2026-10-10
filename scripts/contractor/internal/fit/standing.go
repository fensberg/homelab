package fit

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// The label a control plane carries. A machine without it is a worker.
const controlPlane = "node-role.kubernetes.io/control-plane"

// The priority class of work that can wait, which is not counted.
const canWait = "batch"

// Standing is a site as its own cluster describes it: from the list of its
// machines and the list of its pods, as `kubectl get -o json` gives them.
//
// Read from the cluster and not from what is declared, because only the
// cluster knows what is really running: a chart makes pods its settings do
// not mention, and the system's own pods are declared nowhere here.
//
// In whole gibibytes. These are figures to plan by, kept in a record the
// estate is held to matching, and a figure that moved with every megabyte
// would never match it twice. What the workers hold is rounded down, each
// worker by itself, since part of a gibibyte is not room to promise; the
// largest worker and what is reserved are rounded up, since both are taken
// away from that room.
func Standing(nodes, pods []byte) (Site, error) {
	var machines struct {
		Items []struct {
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Status struct {
				Allocatable map[string]string `json:"allocatable"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(nodes, &machines); err != nil {
		return Site{}, fmt.Errorf("the list of machines is not what kubectl gives: %w", err)
	}
	var site Site
	workers := map[string]bool{}
	for _, m := range machines.Items {
		if _, is := m.Metadata.Labels[controlPlane]; is {
			continue
		}
		holds, err := Bytes(m.Status.Allocatable["memory"])
		if err != nil {
			return Site{}, fmt.Errorf("the machine %s: what it holds for pods: %w", m.Metadata.Name, err)
		}
		workers[m.Metadata.Name] = true
		site.Workers += holds / gibibyte * gibibyte
		if up := roundUp(holds); up > site.LargestWorker {
			site.LargestWorker = up
		}
	}
	if len(workers) == 0 {
		return Site{}, fmt.Errorf("the cluster lists %d machine(s) and none is a worker, so there is nothing to hold a change against", len(machines.Items))
	}

	var running struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				NodeName          string `json:"nodeName"`
				PriorityClassName string `json:"priorityClassName"`
				Containers        []struct {
					Resources struct {
						Requests map[string]string `json:"requests"`
					} `json:"resources"`
				} `json:"containers"`
			} `json:"spec"`
			Status struct {
				Phase string `json:"phase"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(pods, &running); err != nil {
		return Site{}, fmt.Errorf("the list of pods is not what kubectl gives: %w", err)
	}
	var reserved int64
	for _, p := range running.Items {
		if p.Status.Phase != "Running" || !workers[p.Spec.NodeName] || p.Spec.PriorityClassName == canWait {
			continue
		}
		for _, c := range p.Spec.Containers {
			written, asks := c.Resources.Requests["memory"]
			if !asks {
				continue
			}
			n, err := Bytes(written)
			if err != nil {
				return Site{}, fmt.Errorf("the pod %s: what it reserves: %w", p.Metadata.Name, err)
			}
			reserved += n
		}
	}
	site.MustRun = roundUp(reserved)
	return site, nil
}

func roundUp(b int64) int64 { return (b + gibibyte - 1) / gibibyte * gibibyte }

// Bytes is an amount of memory as Kubernetes writes one: a number, with a
// power of two (Ki, Mi, Gi, Ti) or of ten (k, M, G, T) after it, or an
// exponent. Anything else is refused: a size read as nothing is a thing
// that reserves nothing, and it does not.
func Bytes(written string) (int64, error) {
	scales := []struct {
		suffix string
		by     float64
	}{
		{"Ki", 1 << 10}, {"Mi", 1 << 20}, {"Gi", 1 << 30}, {"Ti", 1 << 40},
		{"k", 1e3}, {"M", 1e6}, {"G", 1e9}, {"T", 1e12},
	}
	number, by := written, 1.0
	for _, s := range scales {
		if strings.HasSuffix(written, s.suffix) {
			number, by = strings.TrimSuffix(written, s.suffix), s.by
			break
		}
	}
	n, err := strconv.ParseFloat(number, 64)
	if err != nil || n < 0 || math.IsInf(n, 0) || math.IsNaN(n) {
		return 0, fmt.Errorf("%q is not an amount of memory", written)
	}
	return int64(math.Round(n * by)), nil
}

// File is where a site's standing is kept, beside its as-built record.
//
// Beside the record and not in it. The record is the estate's state, which
// the estate is held to matching, and a reading of what is running is the
// same from one night to the next only by luck: a figure kept there would
// have the estate reported as changed whenever a pod was added. So it is a
// file of its own, written whenever a record is taken, after the cluster has
// settled - which is what makes it the site as the last change left it.
const File = "standing.json"

// kept is the file's shape: whole gibibytes, and when they were read.
type kept struct {
	WorkersGiB       int64  `json:"workers_gib"`
	LargestWorkerGiB int64  `json:"largest_worker_gib"`
	MustRunGiB       int64  `json:"must_run_gib"`
	Taken            string `json:"taken"`
}

// Marshal is a standing as the file holds it.
func Marshal(site Site, taken string) ([]byte, error) {
	return json.MarshalIndent(kept{
		WorkersGiB:       site.Workers / gibibyte,
		LargestWorkerGiB: site.LargestWorker / gibibyte,
		MustRunGiB:       site.MustRun / gibibyte,
		Taken:            taken,
	}, "", "  ")
}

// Unmarshal reads the file back, and when it was taken.
func Unmarshal(raw []byte) (Site, string, error) {
	var k kept
	if err := json.Unmarshal(raw, &k); err != nil {
		return Site{}, "", fmt.Errorf("the site's standing is not what a record holds: %w", err)
	}
	return Site{Workers: k.WorkersGiB * gibibyte, LargestWorker: k.LargestWorkerGiB * gibibyte, MustRun: k.MustRunGiB * gibibyte}, k.Taken, nil
}
