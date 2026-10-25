package repo

import (
	"testing"
)

// The untrusted zone has an off switch, and it has to keep working.
//
// The zone exists to host a workload that will one day be deprecated. The whole
// reason for building it this way is that deprecating that workload is a config
// change and a converge, not a rebuild - so an estate that declares no
// untrusted workload must build nothing for one: no subnet, no second image, no
// machine.
//
// That property is easy to state and easy to lose. One resource keyed off the
// site rather than off the machines, or one Ansible task that forgets its gate,
// and the estate is quietly maintaining a zone nobody asked for. Neither
// mistake announces itself: everything still converges, and the residue only
// turns up when somebody wonders what vnetdmz is for months later.
//
// See docs/epochs/03-workload.md, "Removing the untrusted zone".
//
// That every zone resource is keyed off the zone is a rule over the plan
// (policy/conftest/rules/plan_zone_off_switch/plan_zone_off_switch.rego); the playbook's half is here.

// Every zone task in the playbook loops over the zones.
//
// The loop IS the off switch, and that is a stronger property than a gate. A
// `when:` has to be remembered on every task and is exactly the line somebody
// adding a fourth one copies without; a loop over an empty list runs zero
// times because there is nothing to run it against. Ansible creates and never
// removes, so a task that runs unconditionally leaves a vnet, a subnet and a
// VXLAN identifier behind on an estate with nothing to put on them.
//
// This guard replaced one asserting a `dmz_count` gate. That test matched the
// spelling of a variable rather than the property, so reshaping the config from
// a count to named zones broke it while the behaviour it cared about was
// unchanged - which is the failure the mutation ledger exists to make visible.
func TestEveryZoneTaskLoopsOverTheZones(t *testing.T) {
	t.Parallel()
	var checked int
	for _, task := range hypervisorPlay(t).Tasks {
		if !task.Uses("item.vnet") {
			continue
		}
		checked++
		if task.Loop == nil && task.LoopExpr == "" {
			t.Errorf(`the task "%s" reads a zone's values without looping over the zones.

Without the loop it runs once against an undefined item on every estate,
including one with no untrusted workload at all. The loop is what makes "no
zones" mean "nothing is built" - Ansible creates and never removes, so anything
this leaves behind stays until somebody finds it and wonders what it is for.`,
				task.Name)
		}
	}
	if checked == 0 {
		t.Fatal("no task in hypervisor-prep.yml reads a zone's values, so this test proves nothing.\n\n" +
			"Either the zones' SDN tasks were removed or their loop variable was renamed.")
	}
}

// hypervisorPlaybook is the playbook that prepares a hypervisor.
const hypervisorPlaybook = "management/hypervisor/hypervisor-prep.yml"
