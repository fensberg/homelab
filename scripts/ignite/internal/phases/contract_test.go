package phases

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"testing"

	"homelab/ignite/internal/tfsource"
)

// buildStateConnStr (sterilize.go) rebuilds the state database's connection
// string from first principles, because the break-glass path it serves has to
// work at the exact moment Terraform can no longer reach its own state. That
// means restating values variables.tf also declares - a duplication the
// function's own comment acknowledges and accepts.
//
// These tests are what make it safe to accept. Change the NodePort in
// variables.tf without changing sterilize.go and the emergency destroy would
// dial the wrong port at the only moment it matters, having already migrated
// state back out of a cluster it is about to tear down. That failure is
// invisible until it happens; this makes it visible on the pull request.

func clusterFile(t *testing.T, name string) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine this source file's location")
	}
	// <root>/scripts/ignite/internal/phases/contract_test.go
	root := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "CLAUDE.md")); err != nil {
		t.Fatalf("computed repo root %s does not look like the repository: %v", root, err)
	}
	src, err := tfsource.Read(filepath.Join(root, "management", "cluster", name))
	if err != nil {
		t.Fatalf("reading the OpenTofu source: %v", err)
	}
	return src
}

func TestContract_StateDatabaseLocalsMatchTheOpenTofuSource(t *testing.T) {
	src := clusterFile(t, "variables.tf")

	if got, err := tfsource.Int(src, "state_db_nodeport"); err != nil {
		t.Errorf("variables.tf: %v", err)
	} else if got != stateDBNodePort {
		t.Errorf("state_db_nodeport: variables.tf says %d, sterilize.go says %d.\n\nThe emergency destroy dials this port to migrate state back out of the cluster before tearing it down. A stale value here fails at the one moment there is no second chance.", got, stateDBNodePort)
	}

	for _, tc := range []struct{ local, go_ string }{
		{"state_db_name", stateDBName},
		{"state_db_owner", stateDBOwner},
	} {
		got, err := tfsource.String(src, tc.local)
		if err != nil {
			t.Errorf("variables.tf: %v", err)
			continue
		}
		if got != tc.go_ {
			t.Errorf("%s: variables.tf says %q, sterilize.go says %q", tc.local, got, tc.go_)
		}
	}
}

// node_ips is a for-expression, not a literal, so this reads the offset out of
// it rather than pretending tfsource can evaluate HCL. Deliberately narrow: if
// the expression is restructured, the lookup stops matching and the test says
// so, which is the correct outcome - the contract needs a human then, not a
// cleverer regex.
var (
	nodeIPOffset       = regexp.MustCompile(`cidrhost\(local\.node_cidr,\s*(\d+)\s*\+\s*i\)`)
	nodeCIDRThirdOctet = regexp.MustCompile(`node_cidr\s*=\s*"10\.\$\{local\.octet\}\.(\d+)\.0/24"`)
)

func TestContract_FirstControlPlaneHostMatchesTheOpenTofuSource(t *testing.T) {
	src := clusterFile(t, "variables.tf")

	m := nodeIPOffset.FindStringSubmatch(src)
	if m == nil {
		t.Fatal("could not find the cidrhost(local.node_cidr, N + i) offset in variables.tf.\n\nnode_ips was restructured. sterilize.go hard-codes the first control-plane host to reach the state database during an emergency destroy; confirm by hand that it still matches, then update this test to the new shape.")
	}
	offset, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("offset %q is not an integer: %v", m[1], err)
	}
	if offset != stateDBFirstNodeHost {
		t.Errorf("first control-plane host offset: variables.tf says %d, sterilize.go says %d", offset, stateDBFirstNodeHost)
	}

	// The third octet is the other half of the same address. sterilize.go
	// builds "10.<octet>.10.<host>"; if the node subnet ever moves off .10,
	// that string is wrong in a way nothing else would catch.
	c := nodeCIDRThirdOctet.FindStringSubmatch(src)
	if c == nil {
		t.Fatal("could not find node_cidr's \"10.${local.octet}.N.0/24\" shape in variables.tf - see the note above")
	}
	if c[1] != "10" {
		t.Errorf("node_cidr's third octet is %s, but sterilize.go builds the state database address as \"10.%%d.10.%%d\"", c[1])
	}
}

// AllPhases is the sequence main.go's -from flag slices into, and the switch
// in registry.go is what dispatches each name. A phase listed in one and not
// the other is a panic at run time - on a phase that, by definition, someone
// explicitly asked for with -phase or -from.
//
// Checked by reading the switch rather than by calling Run: every phase has
// real side effects (op read, tofu apply, deleting state), so a test that
// actually invoked them to see whether they panic would be a test that
// sterilizes the developer's workspace to prove a typo.
func TestContract_EveryPhaseInTheSequenceDispatches(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine this source file's location")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "registry.go"))
	if err != nil {
		t.Fatalf("reading registry.go: %v", err)
	}

	dispatched := map[string]bool{}
	for _, m := range switchCase.FindAllStringSubmatch(string(data), -1) {
		dispatched[m[1]] = true
	}
	if len(dispatched) == 0 {
		t.Fatal("found no `case \"...\":` arms in registry.go - the dispatch switch was restructured and this test can no longer see it")
	}

	for _, name := range AllPhases {
		if !dispatched[name] {
			t.Errorf("phase %q is in AllPhases but has no case arm in registry.go: `-phase %s` would panic", name, name)
		}
		delete(dispatched, name)
	}
	for name := range dispatched {
		t.Errorf("registry.go dispatches phase %q, which is not in AllPhases: it can never run as part of a sequence and `-phase %s` is undocumented", name, name)
	}
}

var switchCase = regexp.MustCompile(`(?m)^\s*case\s+"([a-z]+)":`)
