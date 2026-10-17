package rules.plan_writer_test

import data.rules.plan_writer
import rego.v1

plan := {"permissions": {"contents": "read"}, "steps": [{"uses": "$/.github/workflows/mobilize"}, {"run": "go build -C scripts/contractor ."}]}

comment := {"permissions": {"pull-requests": "write"}, "steps": [{"uses": "$/.github/workflows/mobilize", "with": {"checkout": "false"}}, {"uses": "actions/github-script@0123"}]}

workflow(jobs) := [{"path": ".github/workflows/plan-infrastructure.yml", "contents": {"jobs": jobs}}]

denies(jobs, words) if {
	some msg in plan_writer.deny with input as workflow(jobs)
	contains(msg, words)
}

test_a_reading_plan_and_a_writer_that_runs_nothing_pass if {
	count(plan_writer.deny) == 0 with input as workflow({"plan": plan, "comment": comment})
}

test_a_plan_that_states_no_permissions_is_refused if {
	denies({"plan": object.remove(plan, ["permissions"]), "comment": comment}, "states no permissions of its own")
}

test_a_plan_that_can_write_is_refused if {
	denies({"plan": object.union(plan, {"permissions": {"contents": "read", "pull-requests": "write"}}), "comment": comment}, "with a token that can write pull-requests")
}

test_a_writer_that_runs_a_command_is_refused if {
	steps := array.concat(comment.steps, [{"name": "Tidy", "run": "rm -rf x"}])
	denies({"plan": plan, "comment": object.union(comment, {"steps": steps})}, `runs a command of its own ("Tidy")`)
}

test_a_writer_that_checks_the_branch_out_is_refused if {
	denies({"plan": plan, "comment": object.union(comment, {"steps": [{"uses": "$/.github/workflows/mobilize", "with": {"checkout": "true"}}]})}, "checks the branch out")
}

test_a_writer_whose_mobilize_says_nothing_checks_out if {
	denies({"plan": plan, "comment": object.union(comment, {"steps": [{"uses": "$/.github/workflows/mobilize"}]})}, "checks the branch out")
}

test_no_plan_job_is_refused if {
	denies({"comment": comment}, "no job of the plan workflow runs the contractor")
}

test_no_writer_is_refused if {
	denies({"plan": plan}, "no job of the plan workflow can write")
}

test_no_plan_workflow_is_refused if {
	some msg in plan_writer.deny with input as []
	contains(msg, "is not there")
}
