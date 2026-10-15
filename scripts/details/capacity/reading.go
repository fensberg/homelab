package capacity

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ControlPlaneLabel is the label Kubernetes puts on a machine that is a
// control plane. A machine without it is a worker, which is the only way a
// worker is told: Kubernetes gives it no label of its own.
const ControlPlaneLabel = "node-role.kubernetes.io/control-plane"

// CanWait is the priority class of work that can wait, which is not counted,
// and PriorityClassKey the key a pod names its class under.
const (
	CanWait          = "batch"
	PriorityClassKey = "priorityClassName"
	// PreemptionKey is the field of a PriorityClass that says whether work of
	// that class may put lower work off its machine to make room.
	PreemptionKey = "preemptionPolicy"
	// PreemptsLower is that field's word for work that may put lower work
	// off its machine, and DryRunOnServer is how kubectl is asked to have
	// the API server judge an object without keeping it.
	PreemptsLower  = "PreemptLowerPriority"
	DryRunOnServer = "--dry-run=server"
)

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

// File is where a site's capacity is kept, beside its as-built record.
//
// Beside the record and not in it. The record is the estate's state, which
// the estate is held to matching, and a reading of what is running is the
// same from one night to the next only by luck: a figure kept there would
// have the estate reported as changed whenever a pod was added. So it is a
// file of its own, written whenever a record is taken, after the cluster has
// settled - which is what makes it the site as the last change left it.
const File = "capacity.json"
