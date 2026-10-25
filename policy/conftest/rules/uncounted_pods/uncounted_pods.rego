# The cluster refuses a pod it cannot count, and this is whether that refusal
# has a way round it.
#
# What the rule asks of a pod is proved against a real cluster by the
# integration tier, which offers it pods and reads the answer. That cannot
# see a rule that is right and applies to less than everything: a namespace
# left out, a label that excuses a pod, a refusal turned down to a warning.
# Each of those is one line, reads as configuration, and makes the site's sum
# untrue for exactly the pods nobody is looking at.
#
# The site's limit is a sum of what pods reserve. A pod let in without a
# reservation or a priority class is one the sum does not hold, and a way out
# for some pods is where such a pod will be put. If something cannot meet the
# rule, give it a reservation and a class.
package rules.uncounted_pods

import data.lib
import rego.v1

policy_name := "uncounted-pods"

policies := [f | some f in lib.objects; f.contents.kind == "ValidatingAdmissionPolicy"; f.contents.metadata.name == policy_name]

bindings := [f | some f in lib.objects; f.contents.kind == "ValidatingAdmissionPolicyBinding"; f.contents.spec.policyName == policy_name]

deny contains msg if {
	count(policies) != 1
	msg := $"{count(policies)} ValidatingAdmissionPolicy named {policy_name} were found, and the refusal of uncounted work is one"
}

deny contains msg if {
	count(bindings) != 1
	msg := $"{count(bindings)} bindings of {policy_name} were found. A policy nothing binds refuses nothing, and two bindings may apply it differently."
}

deny contains msg if {
	some b in bindings
	object.get(b.contents.spec, "validationActions", []) != ["Deny"]
	msg := $"{b.path}: the binding's validationActions are {object.get(b.contents.spec, "validationActions", [])}, not [Deny]: a pod that breaks the rule is told so and let in"
}

deny contains msg if {
	some b in bindings
	some key, _ in b.contents.spec
	not key in {"policyName", "validationActions"}
	msg := $"{b.path}: the binding sets {key}, which chooses which pods the rule applies to"
}

deny contains msg if {
	some p in policies
	object.get(p.contents.spec, "failurePolicy", "") != "Fail"
	msg := $"{p.path}: the policy's failurePolicy is {object.get(p.contents.spec, "failurePolicy", "")}, not Fail: a rule that cannot be evaluated lets the pod in"
}

deny contains msg if {
	some p in policies
	p.contents.spec.matchConditions
	msg := $"{p.path}: the policy sets matchConditions, which excuse a pod from being judged at all"
}

deny contains msg if {
	some p in policies
	some key, _ in object.get(p.contents.spec, "matchConstraints", {})
	key != "resourceRules"
	msg := $"{p.path}: the policy's matchConstraints set {key}, which leaves some pods out"
}

deny contains msg if {
	some p in policies
	not judges_pods(p)
	msg := $"{p.path}: the policy's resourceRules do not name pods, so it judges nothing"
}

judges_pods(p) if {
	some rule in object.get(p.contents.spec, ["matchConstraints", "resourceRules"], [])
	"pods" in object.get(rule, "resources", [])
}
