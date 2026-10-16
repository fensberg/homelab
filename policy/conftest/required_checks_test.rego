package rules.required_checks_test

import data.rules.required_checks
import rego.v1

tf(locals) := {"path": "management/estate/x.tf", "contents": {"locals": locals}}

workflow := {"path": ".github/workflows/a.yml", "contents": {"jobs": {"a-keyed-job": {"runs-on": "x"}, "other": {"name": "A named job"}}}}

test_a_job_is_known_by_its_name_or_its_key if {
	count(required_checks.deny) == 0 with input as [workflow, tf([{"something_else": ["ignored"], "required_checks": ["A named job", "a-keyed-job"]}])]
}

test_a_list_worked_out_elsewhere_is_not_read if {
	i := [workflow, tf([{"required_checks": ["A named job"]}, {"required_checks": "${var.worked_out_elsewhere}"}])]
	required_checks.checks == {"A named job"} with input as i
}

test_a_key_that_has_a_name_is_known_only_by_its_name if {
	some msg in required_checks.deny with input as [workflow, tf([{"required_checks": ["other"]}])]
	contains(msg, `a check named "other"`)
}

test_a_name_no_job_reports_is_refused if {
	some msg in required_checks.deny with input as [workflow, tf([{"required_checks": ["The Plan"]}])]
	contains(msg, "no job in any workflow reports under that name")
}

test_nothing_declared_is_refused if {
	some msg in required_checks.deny with input as [workflow]
	contains(msg, "nothing declares required_checks")
}

test_no_workflow_is_refused if {
	some msg in required_checks.deny with input as [tf([{"required_checks": ["x"]}])]
	contains(msg, "no workflow was read")
}
