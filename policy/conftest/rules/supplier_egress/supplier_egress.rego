# A workflow may not invent its own egress endpoint.
#
# These were restated across fifteen allowlists in nine workflows, so no single
# place answered "what can this repository call out to". Adding one endpoint to
# one workflow was invisible to any review not already reading that file.
package rules.supplier_egress

import data.lib
import rego.v1

list := [f.contents | some f in lib.at("scripts/approved-suppliers.yml")][0]

reaches contains [file, host] if {
	some file, doc in lib.workflows
	walk(doc, [path, v])
	count(path) > 0
	path[count(path) - 1] == "allowed-endpoints"
	is_string(v)
	some host in regex.split(`\s+`, trim_space(v))
	regex.match(`^[a-z0-9.-]+:\d+$`, host)
}

deny contains msg if {
	count(reaches) == 0
	msg := "no allowed-endpoints entries found, so this rule proves nothing"
}

deny contains msg if {
	some [file, host] in reaches
	not host in {h | some g in object.get(list, "endpoints", []); some h in object.get(g, "hosts", [])}
	msg := $"{file} reaches \"{host}\", which is not in the approved suppliers list. Declare it in scripts/approved-suppliers.yml with a reason rather than only in the workflow, so one place answers what this repository calls out to."
}
