# No machine's storage or network is written into the OpenTofu as a name.
#
# Where a host keeps machine disks and downloaded images is a property of
# that host: a second hypervisor on LVM rather than ZFS names its storage
# otherwise. compute.tf once named both sixteen times, so describing a second
# kind of host meant editing code (epoch 02, "A hypervisor's own facts come
# from its config"). Each node's entry in the config now names its
# datastores, and the vnet the nodes sit on is the address plan's. A
# datastore_id or a bridge written as a string is a name that was never
# declared anywhere, which the search of every file for the declared names
# cannot see (tests/go/repo/hypervisor_facts_test.go).
#
# ON THE PLAN'S CONFIGURATION: an argument written as a string is one the
# plan gives as a constant_value, in any block of any resource.
package rules.plan_placements_not_written

import data.lib
import rego.v1

placements := {"datastore_id", "bridge"}

written contains [address, name] if {
	some [_, address, r] in lib.plan_configuration
	walk(r.expressions, [path, v])
	count(path) > 0
	name := path[count(path) - 1]
	name in placements
	is_object(v)
	"constant_value" in object.keys(v)
}

placed contains address if {
	some [_, address, r] in lib.plan_configuration
	walk(r.expressions, [path, _])
	count(path) > 0
	path[count(path) - 1] in placements
}

deny contains msg if {
	count(placed) == 0
	msg := "no resource in any plan's configuration places anything on a datastore or a bridge, so this checked nothing"
}

deny contains msg if {
	some [address, name] in written
	msg := sprintf("%s writes its %s as a string. Read it from the hypervisor node's datastores or the address plan: a name written here was declared nowhere, and a second kind of host means editing code.", [address, name])
}
