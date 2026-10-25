package rules.uncounted_pods_test

import data.rules.uncounted_pods
import rego.v1

policy(spec) := {"path": "p.yaml", "contents": {"kind": "ValidatingAdmissionPolicy", "metadata": {"name": "uncounted-pods"}, "spec": spec}}

binding(spec) := {"path": "p.yaml", "contents": {"kind": "ValidatingAdmissionPolicyBinding", "metadata": {"name": "uncounted-pods"}, "spec": spec}}

good_policy := {"failurePolicy": "Fail", "matchConstraints": {"resourceRules": [{"apiGroups": [""], "apiVersions": ["v1"], "operations": ["CREATE"], "resources": ["pods"]}]}, "validations": [{"expression": "true"}]}

good_binding := {"policyName": "uncounted-pods", "validationActions": ["Deny"]}

denies(p, b, words) if {
	some msg in uncounted_pods.deny with input as [policy(p), binding(b)]
	contains(msg, words)
}

test_the_refusal_as_it_should_be_passes if {
	count(uncounted_pods.deny) == 0 with input as [policy(good_policy), binding(good_binding)]
}

test_a_refusal_turned_to_a_warning_is_refused if {
	denies(good_policy, object.union(good_binding, {"validationActions": ["Warn"]}), "told so and let in")
}

test_a_binding_that_chooses_its_pods_is_refused if {
	denies(good_policy, object.union(good_binding, {"matchResources": {"namespaceSelector": {}}}), "chooses which pods the rule applies to")
}

test_a_policy_that_lets_pods_in_when_it_fails_is_refused if {
	denies(object.union(good_policy, {"failurePolicy": "Ignore"}), good_binding, "cannot be evaluated lets the pod in")
}

test_a_policy_that_excuses_pods_is_refused if {
	denies(object.union(good_policy, {"matchConditions": [{"name": "x", "expression": "true"}]}), good_binding, "excuse a pod from being judged")
}

test_a_policy_that_leaves_namespaces_out_is_refused if {
	denies(object.union(good_policy, {"matchConstraints": {"resourceRules": good_policy.matchConstraints.resourceRules, "namespaceSelector": {}}}), good_binding, "leaves some pods out")
}

test_a_policy_that_judges_no_pod_is_refused if {
	denies(object.union(good_policy, {"matchConstraints": {"resourceRules": [{"resources": ["deployments"]}]}}), good_binding, "do not name pods")
}

test_a_policy_nothing_binds_is_refused if {
	some msg in uncounted_pods.deny with input as [policy(good_policy)]
	contains(msg, "A policy nothing binds refuses nothing")
}
