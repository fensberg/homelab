package rules.plan_required_checks_test

import data.rules.plan_required_checks
import rego.v1

ruleset(names) := {"address": "module.repository[\"r\"].github_repository_ruleset.checks", "type": "github_repository_ruleset", "values": {"rules": [{"required_status_checks": [{"required_check": [{"context": n, "integration_id": 1} | some n in names]}]}]}}

plan(rulesets) := {"path": "estate.plan.json", "contents": {"planned_values": {"root_module": {"child_modules": [{"resources": rulesets}]}}}}

workflow := {"path": ".github/workflows/a.yml", "contents": {"jobs": {"a-keyed-job": {"runs-on": "x"}, "other": {"name": "A named job"}}}}

test_a_job_is_known_by_its_name_or_its_key if {
	count(plan_required_checks.deny) == 0 with input as [workflow, plan([ruleset(["A named job", "a-keyed-job"]), {"address": "r.other", "type": "github_repository_ruleset", "values": {"rules": [{"pull_request": [{}]}]}}])]
}

test_every_ruleset_is_read if {
	i := [workflow, plan([ruleset(["A named job"]), ruleset(["a-keyed-job"])])]
	plan_required_checks.checks == {"A named job", "a-keyed-job"} with input as i
}

test_a_key_that_has_a_name_is_known_only_by_its_name if {
	some msg in plan_required_checks.deny with input as [workflow, plan([ruleset(["other"])])]
	contains(msg, `a check named "other"`)
}

test_a_name_no_job_reports_is_refused if {
	some msg in plan_required_checks.deny with input as [workflow, plan([ruleset(["The Plan"])])]
	contains(msg, "no job in any workflow reports under that name")
}

test_nothing_declared_is_refused if {
	some msg in plan_required_checks.deny with input as [workflow]
	contains(msg, "no ruleset in any plan holds a merge to a check")
}

test_no_workflow_is_refused if {
	some msg in plan_required_checks.deny with input as [plan([ruleset(["x"])])]
	contains(msg, "no workflow was read")
}
