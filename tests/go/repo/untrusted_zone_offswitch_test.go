package repo

import (
	"regexp"
	"strings"
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

// Every zone resource keys off the machines, never off the site.
//
// `for_each = toset(local.dmz_hypervisors)` builds nothing when the zone is
// empty, because that list is derived from the machines. `for_each =
// toset(local.all_vm_hypervisors)` would build the same resource on every
// hypervisor forever, and would read as correct in review - it is the line the
// resource beside it uses.
func TestEveryZoneResourceIsKeyedOffItsMachines(t *testing.T) {
	body := readRepoFile(t, "management/cluster/compute.tf")

	// Top-level blocks, so a resource is judged with its own for_each rather
	// than with a neighbour's.
	blocks := regexp.MustCompile(`(?m)^resource\s+"`).Split(body, -1)

	var checked int
	for _, block := range blocks {
		if !strings.Contains(block, "dmz") {
			continue
		}
		// The header line names the resource; keep it for the message.
		header := strings.SplitN(block, "\n", 2)[0]
		if !strings.Contains(block, "for_each") {
			continue // not a per-machine resource at all
		}
		checked++

		forEach := regexp.MustCompile(`for_each\s*=\s*([^\n]+)`).FindStringSubmatch(block)
		if forEach == nil {
			continue
		}
		if !strings.Contains(forEach[1], "dmz") {
			t.Errorf(`resource "%s is a zone resource keyed off %s.

That builds it whether or not any untrusted machine exists, so an estate that
has deprecated its last untrusted workload keeps maintaining the zone. Key it
off a dmz collection - those are derived from the machines and are empty when
there are none.`, header, strings.TrimSpace(forEach[1]))
		}
	}

	if checked == 0 {
		t.Fatal("no zone resource with a for_each found in compute.tf, so this test proves nothing.\n\n" +
			"Either the zone moved or its resources stopped being per-machine.")
	}
}

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
	body := readRepoFile(t, "management/hypervisor/hypervisor-prep.yml")

	tasks := regexp.MustCompile(`(?m)^    - name:`).Split(body, -1)

	var checked int
	for _, task := range tasks {
		// A zone task is one that reads a zone's own values. The shared vnet
		// listing is not one: it lists every vnet, and looping it over the
		// zones would hide the node vnet from the task that creates it.
		if !strings.Contains(task, "item.vnet") && !strings.Contains(task, "item.item.vnet") {
			continue
		}
		checked++

		if !strings.Contains(task, "loop:") {
			name := strings.SplitN(strings.TrimSpace(task), "\n", 2)[0]
			t.Errorf(`the task "%s" reads a zone's values without looping over the zones.

Without the loop it runs once against an undefined item on every estate,
including one with no untrusted workload at all. The loop is what makes "no
zones" mean "nothing is built" - Ansible creates and never removes, so anything
this leaves behind stays until somebody finds it and wonders what it is for.`,
				strings.Trim(name, ": "))
		}
	}

	if checked == 0 {
		t.Fatal("no task in hypervisor-prep.yml reads a zone's values, so this test proves nothing.\n\n" +
			"Either the zones' SDN tasks were removed or their loop variable was renamed.")
	}
}
