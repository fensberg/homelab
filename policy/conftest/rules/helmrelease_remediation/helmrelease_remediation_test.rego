package rules.helmrelease_remediation_test

import data.rules.helmrelease_remediation
import rego.v1

release(spec) := [{"path": "clusters/r.yaml", "contents": {"kind": "HelmRelease", "metadata": {"name": "r"}, "spec": spec}}]

retry := {"strategy": {"name": "RetryOnFailure"}}

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_retrying_on_both_passes if {
	count(helmrelease_remediation.deny) == 0 with input as release({"install": retry, "upgrade": retry})
}

test_remediating_is_refused if {
	denies(helmrelease_remediation.deny, `spec.install.strategy.name = "RemediateOnFailure", not RetryOnFailure`) with input as release({"install": {"strategy": {"name": "RemediateOnFailure"}}, "upgrade": retry})
}

test_saying_nothing_is_refused if {
	denies(helmrelease_remediation.deny, `spec.upgrade.strategy.name = "", not RetryOnFailure`) with input as release({"install": retry})
}

test_no_release_is_refused if {
	some msg in helmrelease_remediation.deny with input as []
	contains(msg, "no HelmRelease was found")
}
