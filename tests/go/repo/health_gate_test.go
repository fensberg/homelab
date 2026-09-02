package repo

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The cluster-health gate must depend on the machines it is asking about.
//
// It is scoped to `local.node_ips` - the nodes the config asks for - which is
// fully known at plan time. Without a dependency on the VMs, a plan that
// changes the machine count reads against addresses with no machine behind
// them, waits, and times out after ten minutes producing nothing. The same
// scoping made a destroy ask whether a three-node cluster was a healthy
// five-node one and spend ten minutes finding out.
//
// OpenTofu defers a data source read to apply time when something it depends
// on has pending changes, so depending on the VMs is what makes this a gate
// rather than an obstruction. Removing that dependency silently reintroduces
// both failures, and neither is visible until somebody changes the node count
// or tears an estate down - which is to say, at the two worst moments.
func TestTheHealthGateDependsOnTheMachines(t *testing.T) {
	body := readClusterHCL(t, "talos.tf")

	block := dataBlock(t, body, "talos_cluster_health")

	const vms = "proxmox_virtual_environment_vm.talos_cp"
	if !strings.Contains(block, vms) {
		t.Errorf("data.talos_cluster_health does not depend on %s.\n\n"+
			"Without it the read happens at plan time against nodes the config "+
			"asks for but that do not exist, so a plan that changes the machine "+
			"count times out after ten minutes and a destroy does the same. See #117.", vms)
	}
}

// Nothing should start reading a value out of this gate. Its only job is the
// depends_on edges pointing at it; an attribute reference would make it load
// bearing at plan time again and undo the fix above.
func TestNothingReadsAValueFromTheHealthGate(t *testing.T) {
	root := filepath.Join(repoRoot(t), "management", "cluster")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("reading the cluster directory: %v", err)
	}

	// An attribute read looks like data.talos_cluster_health.this.<something>.
	attr := regexp.MustCompile(`data\.talos_cluster_health\.this\.\w`)

	var checked int
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tf") {
			continue
		}
		checked++
		body := readClusterHCL(t, e.Name())
		if loc := attr.FindString(body); loc != "" {
			t.Errorf("%s reads %s from the health gate.\n"+
				"That makes it load bearing at plan time again, which is the "+
				"condition that made a plan time out. Use depends_on instead.",
				e.Name(), loc)
		}
	}
	if checked == 0 {
		t.Fatal("no .tf files were examined, so this proves nothing")
	}
}

func readClusterHCL(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "management", "cluster", name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(b)
}

// dataBlock returns the body of the named data block, so an assertion about it
// cannot be satisfied by a mention somewhere else in the file.
func dataBlock(t *testing.T, body, kind string) string {
	t.Helper()
	start := strings.Index(body, `data "`+kind+`"`)
	if start < 0 {
		t.Fatalf(`no data "%s" block found`, kind)
	}
	end := strings.Index(body[start:], "\n}\n")
	if end < 0 {
		t.Fatalf(`data "%s" block is not terminated`, kind)
	}
	return body[start : start+end]
}
