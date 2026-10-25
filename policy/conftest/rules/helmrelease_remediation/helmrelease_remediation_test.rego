package rules.helmrelease_remediation_test

import data.rules.helmrelease_remediation
import rego.v1

release(spec) := [{"path": "clusters/r.yaml", "contents": {"kind": "HelmRelease", "metadata": {"name": "r"}, "spec": spec}}]

retry := {"strategy": {"name": "RetryOnFailure"}}

denies(spec, words) if {
	some msg in helmrelease_remediation.deny with input as release(spec)
	contains(msg, words)
}

test_retrying_on_both_passes if {
	count(helmrelease_remediation.deny) == 0 with input as release({"install": retry, "upgrade": retry})
}

test_remediating_is_refused if {
	denies({"install": {"strategy": {"name": "RemediateOnFailure"}}, "upgrade": retry}, `spec.install.strategy.name = "RemediateOnFailure", not RetryOnFailure`)
}

test_saying_nothing_is_refused if {
	denies({"install": retry}, `spec.upgrade.strategy.name = "", not RetryOnFailure`)
}

test_no_release_is_refused if {
	some msg in helmrelease_remediation.deny with input as []
	contains(msg, "no HelmRelease was found")
}
