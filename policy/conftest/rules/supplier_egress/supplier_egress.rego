# A job may reach only an approved supplier.
#
# These were restated across fifteen allowlists in nine workflows, so no single
# place answered "what can this repository call out to". Each job's hosts are
# declared under egress: in the suppliers list, and every one of them must be
# an endpoint declared there with a reason. The workflows carried a copy of
# each list for harden-runner until it was cut on 2026-11-12 (#795).
package rules.supplier_egress

import data.lib
import rego.v1

list := [f.contents | some f in lib.at("scripts/approved-suppliers.yml")][0]

reaches contains [job, host] if {
	some e in object.get(list, "egress", [])
	job := e.job
	some host in object.get(e, "hosts", [])
}

deny contains msg if {
	count(reaches) == 0
	msg := "no job in the egress section reaches a host, so this rule proves nothing"
}

deny contains msg if {
	some [job, host] in reaches
	not host in {h | some g in object.get(list, "endpoints", []); some h in object.get(g, "hosts", [])}
	msg := $"{job} reaches \"{host}\", which is not in the approved suppliers list. Declare it under endpoints: with a reason, so one place answers what this repository calls out to."
}
