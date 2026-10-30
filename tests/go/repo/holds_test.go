package repo

import (
	"go/ast"
	"go/token"
	"sort"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"homelab/contractor/config"
	"homelab/contractor/steps"
	"homelab/tests/parsed"
)

// Nothing tears a site down without the safety officer clearing it first.
//
// The officer is another program, so that the one holding the detonator
// cannot be what decides the explosion is safe. That only holds while every
// path to the teardown goes past it, and the next path added is the one that
// will not. So this reads the source: any function that starts a teardown
// asks the officer earlier in its own body.
//
// One path is not asked, and it is not a teardown of a site: a build that
// fails undoes what that same run built, minutes old, before anything on it
// was worth keeping. Asking there would refuse over copies belonging to a
// site that stood before, and leave the half-built machines behind.
func TestNoTeardownStartsUntilTheSafetyOfficerClearsIt(t *testing.T) {
	light(t)
	const teardown, officer, undoesItsOwnBuild = "tearDown", "clearedBySafetyOfficer", "EmergencyDestroy"

	tree := repository(t)
	var starts, asked, exempt int
	for _, rel := range goFiles(t) {
		if strings.HasSuffix(rel, "_test.go") {
			continue
		}
		file := tree.Go(t, token.NewFileSet(), rel)
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			var tornDown, cleared []token.Pos
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if id, ok := call.Fun.(*ast.Ident); ok {
					switch id.Name {
					case teardown:
						tornDown = append(tornDown, call.Pos())
					case officer:
						cleared = append(cleared, call.Pos())
					}
				}
				return true
			})
			if len(tornDown) == 0 {
				continue
			}
			starts++
			if fn.Name.Name == undoesItsOwnBuild {
				exempt++
				continue
			}
			sort.Slice(tornDown, func(i, j int) bool { return tornDown[i] < tornDown[j] })
			if len(cleared) == 0 || cleared[0] > tornDown[0] {
				t.Errorf(`%s: %s starts a teardown without asking the safety officer first.

Whatever this destroys may hold the only copy of something that should outlive
it, and the officer is the only party that looks. Call %s before %s, and
return what it returns.`, rel, fn.Name.Name, officer, teardown)
				continue
			}
			asked++
		}
	}
	if starts == 0 || asked == 0 {
		t.Fatalf("found %d function(s) that start a teardown and %d that ask the officer first, so this is no longer reading the teardown", starts, asked)
	}
	if exempt > 1 {
		t.Errorf("%d functions are named %s; one build undoes itself, and no more", exempt, undoesItsOwnBuild)
	}
}

// What each machine is given is written twice until a site's config says
// it: where the cluster module makes the machine, and where the contractor
// budgets a hypervisor's memory before anything is built. If the two drift,
// the budget passes a site its host cannot hold, or refuses one it can.
func TestAMachinesMemoryIsTheSameWhereItIsMadeAndWhereItIsBudgeted(t *testing.T) {
	light(t)
	tree := repository(t)
	for resource, role := range map[string]config.MachineRole{
		"talos_cp": config.ControlPlane, "talos_worker": config.Worker, "dmz": config.Untrusted,
	} {
		machine := tree.Declaring(t, "resource", steps.MachineResource, resource)
		given := parsed.Attribute(machine.Body, "memory", "dedicated")
		if given == nil {
			t.Fatalf("the machine %s is given no memory", resource)
		}
		// A machine whose size is also what rebuilds it reads its memory from
		// a local both share (the zone's machine): the figure is there.
		v, ok := parsed.Literal(tree.Resolve(t, machine.Dir(), given.Expr))
		if !ok || v.IsNull() || v.Type() != cty.Number {
			t.Fatalf("the machine %s is given memory that is not a figure written out: %s", resource, tree.Source(t, given.Expr))
		}
		mebibytes, _ := v.AsBigFloat().Int64()
		if made, budgeted := mebibytes<<20, config.MemoryOf[role]; made != budgeted {
			t.Errorf("a %s is made with %d MiB and budgeted at %d MiB. The budget is what refuses a site its host cannot hold, and it is holding the wrong figure", role, made>>20, budgeted>>20)
		}
	}
}

// A second hypervisor reopens how idle capacity is lent.
//
// The decision was put off on 2026-10-08, with two things that would reopen
// it. One is seen in the cluster and is an alert
// (LendingIdleCapacityNeedsAccounting). The other is seen here: a site given
// a second hypervisor. With one host, every machine shares the same memory
// and lending what is idle is a matter of who is stopped first. With two,
// where work that can wait is put starts to matter, and that is the
// accounting Koordinator does and Kubernetes unaided does not.
//
// This fails on the change that adds the second hypervisor, so the question
// is asked by whoever is adding it and not remembered by anybody. It is
// answered by changing this test: to the decision that was made.
func TestASecondHypervisorReopensHowIdleCapacityIsLent(t *testing.T) {
	light(t)
	// Read as the program reads it, so this is not a second idea of what
	// the config holds.
	var template config.Config
	repository(t).JSONInto(t, "config/management.tpl.json", &template)
	if len(template.Sites) == 0 {
		t.Fatal("the config template declares no site, so this looked at nothing")
	}
	for name, site := range template.Sites {
		if len(site.Hypervisor.Nodes) == 0 {
			t.Errorf("the site %s declares no hypervisor, so this looked at nothing for it", name)
		}
		if len(site.Hypervisor.Nodes) > 1 {
			t.Errorf(`the site %s has %d hypervisors, which reopens a decision that was put off.

Idle capacity is lent with no accounting: work that can wait reserves little,
runs in what is free and is stopped when the memory is wanted back. That was
chosen for a site with one host. With a second, where such work is placed
matters, and two projects that account for it were looked at and not taken up:
Koordinator, which is maintained, and Crane, which is not and whose prediction
of a workload's cycles is still the part worth reading.

Decide whether to trial Koordinator before this site grows, then change this
test to say what was decided. What was found is beside the alert
LendingIdleCapacityNeedsAccounting.`, name, len(site.Hypervisor.Nodes))
		}
	}
}
