package repo

import (
	"homelab/details/tofufiles"
	"regexp"
	"testing"
)

// Nothing should start reading a value out of the cluster-health gate. Its
// only job is the depends_on edges pointing at it, on the machines among
// them (policy/conftest/health_gate.rego); an attribute reference would make
// it load bearing at plan time again and undo that.
func TestNothingReadsAValueFromTheHealthGate(t *testing.T) {
	// An attribute read looks like data.talos_cluster_health.this.<something>.
	attr := regexp.MustCompile(`data\.talos_cluster_health\.this\.\w`)

	// Every OpenTofu file, not the files of one root: wherever the gate is
	// declared, nothing anywhere may read a value out of it.
	for rel, body := range tofuSources(t) {
		if loc := attr.FindString(tofufiles.Code(body)); loc != "" {
			t.Errorf("%s reads %s from the health gate.\n"+
				"That makes it load bearing at plan time again, which is the "+
				"condition that made a plan time out. Use depends_on instead.",
				rel, loc)
		}
	}
}
