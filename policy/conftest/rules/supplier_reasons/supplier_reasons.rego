# The approved suppliers list is the estate's declaration of every outside
# party it may take delivery from. These rules are what make it a rule rather
# than a document: every supplier on it gives a reason (held here), and what
# the estate takes is on it - its images (rules.supplier_images), its charts
# (rules.supplier_charts), the hosts a workflow reaches
# (rules.supplier_egress), its commit hooks (rules.supplier_hooks) and its
# providers (rules.supplier_providers).
#
# The incident behind it: an unpinned third-party image was proposed for a
# live cluster, with host networking, on a control-plane node, and nothing in
# this repository would have objected. The image was very likely harmless.
# "Very likely harmless" is the reasoning this estate exists to replace.
#
# Scope, stated because a guard nobody understands the edges of is worse than
# none: this catches what is COMMITTED. It cannot see a command pasted into a
# terminal, and it is not admission control. See scripts/approved-suppliers.yml.
package rules.supplier_reasons

import data.lib
import rego.v1

list := [f.contents | some f in lib.at("scripts/approved-suppliers.yml")][0]

# Every reason has to be filled in. An entry with no reason is somebody
# widening the estate's supply chain without saying why, which is the review
# this file exists to force.

blank(entry, key) if trim_space($"{object.get(entry, key, "")}") == ""

deny contains msg if {
	count(object.get(list, "registries", [])) == 0
	msg := "the suppliers list declares no registries, so every rule here proves nothing"
}

deny contains msg if {
	count(object.get(list, "endpoints", [])) == 0
	msg := "the suppliers list declares no endpoints, so every rule here proves nothing"
}

deny contains msg if {
	some r in object.get(list, "registries", [])
	blank(r, "reason")
	msg := $"registry \"{r.host}\" is approved with no reason given"
}

deny contains msg if {
	some ch in object.get(list, "charts", [])
	blank(ch, "reason")
	msg := $"chart source \"{ch.url}\" is approved with no reason given"
}

deny contains msg if {
	some g in object.get(list, "endpoints", [])
	blank(g, "reason")
	msg := $"endpoint group \"{g.group}\" is approved with no reason given"
}

deny contains msg if {
	some e in object.get(list, "exemptions", [])
	blank(e, "reason")
	msg := $"exemption for \"{e.path}\" gives no reason"
}

deny contains msg if {
	some e in object.get(list, "exemptions", [])
	blank(e, "trigger")
	msg := $"exemption for \"{e.path}\" gives no trigger, so nothing says when it ends"
}

deny contains msg if {
	some e in object.get(list, "exemptions", [])
	not object.get(e, "issue", 0) > 0
	msg := $"exemption for \"{e.path}\" names no issue. File the work it defers and put the number in issue:, or fix the thing instead - a red check is not on its own a reason to write an excuse."
}

deny contains msg if {
	some p in object.get(list, "providers", [])
	blank(p, "reason")
	msg := $"provider \"{p.source}\" is approved with no reason given"
}
