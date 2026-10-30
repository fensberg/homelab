package rules.plan_writer_test

import data.rules.plan_writer
import rego.v1

plan := {"permissions": {"contents": "read"}, "steps": [{"uses": "$/.github/workflows/mobilize"}, {"run": "go build -C scripts/contractor ."}]}

comment := {"permissions": {"pull-requests": "write"}, "steps": [{"uses": "$/.github/workflows/mobilize", "with": {"checkout": "false"}}, {"uses": "actions/github-script@0123"}]}

workflow(jobs) := [{"path": ".github/workflows/plan-infrastructure.yml", "contents": {"jobs": jobs}}]

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_a_reading_plan_and_a_writer_that_runs_nothing_pass if {
	count(plan_writer.deny) == 0 with input as workflow({"plan": plan, "comment": comment})
}

test_a_plan_that_states_no_permissions_is_refused if {
	denies(plan_writer.deny, "states no permissions of its own") with input as workflow({"plan": object.remove(plan, ["permissions"]), "comment": comment})
}

test_a_plan_that_can_write_is_refused if {
	denies(plan_writer.deny, "with a token that can write pull-requests") with input as workflow({"plan": object.union(plan, {"permissions": {"contents": "read", "pull-requests": "write"}}), "comment": comment})
}

test_a_writer_that_runs_a_command_is_refused if {
	steps := array.concat(comment.steps, [{"name": "Tidy", "run": "rm -rf x"}])
	denies(plan_writer.deny, `runs a command of its own ("Tidy")`) with input as workflow({"plan": plan, "comment": object.union(comment, {"steps": steps})})
}

test_a_writer_that_checks_the_branch_out_is_refused if {
	denies(plan_writer.deny, "checks the branch out") with input as workflow({"plan": plan, "comment": object.union(comment, {"steps": [{"uses": "$/.github/workflows/mobilize", "with": {"checkout": "true"}}]})})
}

test_a_writer_whose_mobilize_says_nothing_checks_out if {
	denies(plan_writer.deny, "checks the branch out") with input as workflow({"plan": plan, "comment": object.union(comment, {"steps": [{"uses": "$/.github/workflows/mobilize"}]})})
}

test_no_plan_job_is_refused if {
	denies(plan_writer.deny, "no job of the plan workflow runs the contractor") with input as workflow({"comment": comment})
}

test_no_writer_is_refused if {
	denies(plan_writer.deny, "no job of the plan workflow can write") with input as workflow({"plan": plan})
}

test_no_plan_workflow_is_refused if {
	some msg in plan_writer.deny with input as []
	contains(msg, "is not there")
}
