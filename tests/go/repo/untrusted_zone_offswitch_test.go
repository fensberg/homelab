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

// Every zone task in the playbook is gated on the count.
//
// Ansible creates and does not remove, so a task that runs unconditionally
// leaves a vnet, a subnet and a VXLAN identifier behind on an estate with
// nothing to put on them. The gate is one line per task and is exactly the
// line somebody adding a fourth task copies without.
func TestEveryZoneTaskIsGatedOnTheCount(t *testing.T) {
	body := readRepoFile(t, "management/hypervisor/hypervisor-prep.yml")

	// Task boundaries. The playbook writes every task as "    - name:".
	tasks := regexp.MustCompile(`(?m)^    - name:`).Split(body, -1)

	var checked int
	for _, task := range tasks {
		// A task is a zone task if it touches the zone's own variables. The
		// shared vnet listing is not one: it lists every vnet, and gating it
		// would hide the node vnet from the task that creates it.
		if !strings.Contains(task, "sdn_dmz_") {
			continue
		}
		checked++

		if !strings.Contains(task, "dmz_count") {
			name := strings.SplitN(strings.TrimSpace(task), "\n", 2)[0]
			t.Errorf(`the task "%s" touches the untrusted zone and is not gated on dmz_count.

Ansible creates and never removes, so this runs on an estate with no untrusted
workload and leaves a vnet, a subnet and a VXLAN identifier that nothing uses
and nobody will recognise later.`, strings.Trim(name, ": "))
		}
	}

	if checked == 0 {
		t.Fatal("no task in hypervisor-prep.yml touches the zone's variables, so this test proves nothing.\n\n" +
			"Either the zone's SDN tasks were removed or its variables were renamed.")
	}
}
