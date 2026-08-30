package phases

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"homelab/steward/internal/tfsource"
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
	// <root>/scripts/steward/internal/phases/contract_test.go
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

	// Both sequences, not just ignition. A phase belongs to at least one of
	// them - attach only ever runs in a converge, migrate only in an ignition
	// - so the contract is that dispatch and the union agree exactly, in both
	// directions.
	for _, seq := range [][]string{AllPhases, ConvergePhases} {
		for _, name := range seq {
			if !dispatched[name] {
				t.Errorf("phase %q is in a sequence but has no case arm in registry.go: `-phase %s` would panic", name, name)
			}
		}
	}
	for _, seq := range [][]string{AllPhases, ConvergePhases} {
		for _, name := range seq {
			delete(dispatched, name)
		}
	}
	for name := range dispatched {
		t.Errorf("registry.go dispatches phase %q, which is in neither AllPhases nor ConvergePhases: it can never run as part of a sequence and `-phase %s` is undocumented", name, name)
	}
}

var switchCase = regexp.MustCompile(`(?m)^\s*case\s+"([a-z]+)":`)

// The tailnet key's expiry and the Overlay phase's force-replacement are one
// mechanism split across two languages: a short expiry is only safe because
// every run mints a new key, and forcing a replacement every run is only worth
// the churn because the expiry is short. Either one alone is a regression, and
// nothing else would notice - a key that quietly outlives its use is not an
// error anywhere, it is just a credential nobody revoked.
func TestContract_TailnetKeyExpiryStaysShort(t *testing.T) {
	src := clusterFile(t, "overlay-network.tf")

	got, err := tfsource.Int(src, "overlay_key_expiry_seconds")
	if err != nil {
		t.Fatalf("overlay-network.tf: %v", err)
	}

	const maxSeconds = 3600
	if got > maxSeconds {
		t.Errorf(`overlay_key_expiry_seconds is %d, which is longer than an hour (%d).

The key is used once, at the hypervisor's 'tailscale up', minutes after it is
minted. A tagged device does not expire, so nothing downstream needs the key to
stay valid - a long expiry only leaves a pre-authorized, route-approving
credential sitting in the Tailscale console. Four had accumulated by the end of
epoch 01.

If this genuinely has to grow, the Overlay phase's -replace (overlay.go) is the
thing that makes a short one safe; read that first.`, got, maxSeconds)
	}

	if !strings.Contains(src, "local.overlay_key_expiry_seconds") {
		t.Error("overlay-network.tf declares overlay_key_expiry_seconds but the key resource does not use it")
	}
}

// Converge is the sequence that runs against an estate that already exists, so
// the two phases it must never contain are the ones that assume it does not.
// migrate copies local state over the backend with -force-copy; a converge's
// local state is empty by construction, so running it would erase the estate's
// own record of itself. This is asserted here rather than left to the sequence
// literal, because that literal is one careless edit away from being wrong and
// nothing else would notice until it ran against something real.
func TestContract_ConvergeExcludesMigrate(t *testing.T) {
	for _, banned := range []string{"migrate"} {
		for _, p := range ConvergePhases {
			if p == banned {
				t.Fatalf("ConvergePhases contains %q, which overwrites the state of the estate being converged", banned)
			}
		}
	}

	var hasAttach bool
	for _, p := range ConvergePhases {
		if p == "attach" {
			hasAttach = true
		}
	}
	if !hasAttach {
		t.Fatal("ConvergePhases has no attach phase, so it would start from an empty workspace and plan a second estate beside the real one")
	}

	// Ignition creates the cluster that holds the state, so it cannot begin by
	// connecting to it.
	for _, p := range AllPhases {
		if p == "attach" {
			t.Fatal("AllPhases contains attach: ignition cannot attach to state held by a cluster it has not built yet")
		}
	}
}
