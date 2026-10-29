# A zone's machine carries its zone twice: as a taint, which keeps other work
# off it, and as a label, which is what the work meant for it selects.
#
# WHY BOTH. A toleration lets a pod onto a tainted machine and does not send
# it there. Work meant for an untrusted zone that could only tolerate would
# land wherever there was room, and the machines with room are the ones the
# estate trusts. A machine with the label and no taint is the other half of
# the same mistake: anything may land on it.
#
# AND CLOSED UNTIL IT IS CLEAN. A zone's machine joins with a second taint,
# which nothing tolerates, and keeps it until its clean snapshot is taken; a
# machine that ran a job first would have that job's leavings in the state
# every later job is promised is clean.
#
# ON WHAT TOFU WOULD MAKE. Each machine is read off a site's plan as Talos
# is given it - its patches, as they are written to it - with the keys from
# the fact every language reads them from, so what is held is what a machine
# is given and not how a line of the module is written.
package rules.plan_zone_dedication

import data.lib
import rego.v1

zone := lib.facts["untrusted-zone"]

machines contains [r.address, docs] if {
	some [path, r] in lib.plan
	lib.plan_scope(path) == "site"
	r.type == "talos_machine_configuration"
	docs := [d | some p in object.get(r.values, "config_patches", []); is_string(p); d := yaml.unmarshal(p)]
}

# taint is a taint as the kubelet is handed it, key=value:effect.
taint(written) := {"key": key, "value": value, "effect": effect} if {
	[key, rest] := split(written, "=")
	[value, effect] := split(rest, ":")
}

taints(docs) := {taint(t) |
	some d in docs
	some t in split(d.machine.kubelet.extraArgs["register-with-taints"], ",")
}

labels(docs) := object.union_n([d.machine.nodeLabels | some d in docs; is_object(d.machine.nodeLabels)])

# A machine is a zone's when it carries either half of the zone's key.
dedicated contains [address, docs] if {
	some [address, docs] in machines
	some t in taints(docs)
	t.key == zone.key
}

dedicated contains [address, docs] if {
	some [address, docs] in machines
	labels(docs)[zone.key]
}

deny contains msg if {
	some [address, docs] in dedicated
	not labels(docs)[zone.key]
	msg := $"{address} is kept for a zone and not labelled {zone.key}, so the work meant for it cannot be sent there and lands on a machine the estate trusts"
}

deny contains msg if {
	some [address, docs] in dedicated
	sent := labels(docs)[zone.key]
	count({t | some t in taints(docs); t.key == zone.key; t.value == sent; t.effect == "NoSchedule"}) == 0
	msg := $"{address} is labelled for the zone {sent} and not tainted {zone.key}={sent}:NoSchedule, so other work is not kept off it"
}

deny contains msg if {
	some [address, docs] in dedicated
	count({t | some t in taints(docs); t.key == zone.not_yet_clean; t.value == "true"; t.effect == "NoSchedule"}) == 0
	msg := $"{address} is a zone's and does not join tainted {zone.not_yet_clean}=true:NoSchedule, so it takes work before it has a clean snapshot to be put back to"
}

deny contains msg if {
	count(dedicated) == 0
	msg := "no machine in a site's plan is kept for an untrusted zone, so nothing here was held"
}

deny contains msg if {
	not zone.key
	msg := "config/facts/untrusted-zone.json gives no key, so no machine can be told to be a zone's"
}

deny contains msg if {
	zone.not_yet_clean == zone.key
	msg := $"config/facts/untrusted-zone.json gives the not-yet-clean taint the zone's own key, {zone.key}: lifting it once the machine is clean would lift what keeps other work off the machine too"
}
