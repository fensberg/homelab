# Every resource of the untrusted zone is made once per thing of the zone,
# so a site with no untrusted machine builds nothing for one.
#
# The zone exists to host a workload that will one day be deprecated, and
# the reason for building it this way is that deprecating that workload is
# a config change and a converge, not a rebuild. `for_each =
# toset(local.dmz_hypervisors)` builds nothing when the zone is empty,
# because that list is derived from the machines. `for_each =
# toset(local.all_vm_hypervisors)` would build the same resource on every
# hypervisor forever, and would read as correct in review - it is the line
# the resource beside it uses. Nothing announces it: everything still
# converges, and the residue turns up when somebody wonders what vnetdmz is
# for months later. See docs/epochs/03-workload.md, "Removing the untrusted
# zone".
#
# ON THE PLAN'S CONFIGURATION: what each zone resource's for_each is made
# from. A zone's resource or data source is named for it.
package rules.plan_zone_off_switch

import data.lib
import rego.v1

zoned contains [address, r] if {
	some [_, address, r] in lib.plan_configuration
	contains(r.address, "dmz")
	r.for_each_expression
}

keyed_off_the_zone(r) if {
	some ref in object.get(r.for_each_expression, "references", [])
	some part in split(ref, ".")
	contains(part, "dmz")
}

deny contains msg if {
	count(zoned) == 0
	msg := "no zone resource made per member of a collection is in any plan's configuration, so this checked nothing"
}

deny contains msg if {
	some [address, r] in zoned
	not keyed_off_the_zone(r)
	msg := sprintf("%s is a zone resource keyed off something that is not of the zone. That builds it whether or not any untrusted machine exists, so an estate that has deprecated its last untrusted workload keeps maintaining the zone. Key it off a dmz collection - those are derived from the machines and are empty when there are none.", [address])
}
