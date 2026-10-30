# Every machine says how it sheds load (epoch 05, criterion 16): what it holds
# back from pods for itself, and the free memory below which the kubelet puts
# work off it, gently and then at once.
#
# Left at the kubelet's defaults, a machine held back about half of what its
# own services were measured to use and promised pods the rest. On a site run
# full, that is where an eviction comes from that nothing declared.
#
# ON THE PLAN: what each machine is sent, which is what is true whichever
# declaration it came from, and no list of kinds to fall out of date.
package rules.plan_load_shedding

import data.lib
import rego.v1

# The kubelet's own defaults on this operating system. A declaration that
# holds back less is no declaration at all.
default_held_back := {"controlplane": "512Mi", "worker": "384Mi"}

configurations contains r if {
	some [_, r] in lib.plan
	r.type == "talos_machine_configuration"
}

kubelet(r) := [k |
	some patch in object.get(r.values, "config_patches", [])
	is_string(patch)
	k := object.get(yaml.unmarshal(patch), ["machine", "kubelet", "extraConfig"], null)
	k != null
	object.get(k, "systemReserved", null) != null
]

deny contains msg if {
	count(configurations) == 0
	msg := "no talos_machine_configuration is in any plan, so this checked nothing"
}

deny contains msg if {
	some r in configurations
	count(kubelet(r)) != 1
	msg := $"{r.address} declares how it sheds load {count(kubelet(r))} times, and it is once. Undeclared, it sheds load at the kubelet's defaults, about half of what its services use."
}

deny contains msg if {
	some r in configurations
	some k in kubelet(r)
	least := default_held_back[r.values.machine_type]
	units.parse_bytes(k.systemReserved.memory) < units.parse_bytes(least)
	msg := $"{r.address} holds back {k.systemReserved.memory} for itself, less than the kubelet's own default of {least}."
}

deny contains msg if {
	some r in configurations
	some k in kubelet(r)
	soft := units.parse_bytes(k.evictionSoft["memory.available"])
	hard := units.parse_bytes(k.evictionHard["memory.available"])
	soft <= hard
	msg := $"{r.address}'s gentle threshold is not above its hard one, so work is never put off gently."
}

deny contains msg if {
	some r in configurations
	some k in kubelet(r)
	not k.evictionSoftGracePeriod["memory.available"]
	msg := $"{r.address} says how far it may go before work is put off gently, and not for how long."
}
