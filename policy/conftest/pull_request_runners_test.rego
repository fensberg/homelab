package pull_request_runners_test

import data.pull_request_runners
import rego.v1

pool := "homelab-alpha"

fixed := array.concat(
	[
		{"path": "scripts/approved-suppliers.yml", "contents": {"pull_request_pools": {"names": ["builds"]}}},
		{"path": "clusters/core/x.yaml", "contents": {"kind": "HelmRelease", "metadata": {"name": "self-hosted"}, "spec": {"values": {"runnerScaleSetName": "homelab-${SITE}"}}}},
	],
	[{"path": sprintf(".github/workflows/filler%d.yml", [i]), "contents": {"on": {"push": null}, "jobs": {"j": {"runs-on": "ubuntu-latest"}}}} | some i in numbers.range(1, 4)],
)

with_workflow(doc) := array.concat(fixed, [{"path": ".github/workflows/synthetic.yml", "contents": doc}])

on_pr(job) := with_workflow({"on": {"pull_request": null}, "jobs": {"plan": job}})

refusals(i) := {msg | some msg in pull_request_runners.deny with input as i; contains(msg, "runs pull-request code")}

test_a_hosted_runner_is_where_pull_request_code_runs if {
	count(pull_request_runners.deny) == 0 with input as on_pr({"runs-on": "ubuntu-latest"})
}

test_a_declared_pull_request_pool_is_accepted if {
	count(pull_request_runners.deny) == 0 with input as on_pr({"runs-on": "pr-builds-${{ vars.pull_request_runners_site }}"})
}

test_an_undeclared_pool_of_the_same_shape_is_refused if {
	count(refusals(on_pr({"runs-on": "pr-source-${{ vars.pull_request_runners_site }}"}))) == 1
}

test_guarded_and_gated_on_the_management_pool_is_still_refused if {
	some msg in refusals(on_pr({"runs-on": pool, "environment": "management", "if": "github.event.pull_request.head.repo.full_name == github.repository"}))
	contains(msg, "runs pull-request code on homelab-alpha")
	contains(msg, "management pool (homelab-${SITE})")
}

test_a_push_pinned_job_in_a_pull_request_workflow_is_not_reachable if {
	i := with_workflow({"on": {"pull_request": null, "push": {"branches": ["main"]}}, "jobs": {"converge": {"runs-on": pool, "if": "github.event_name == 'push'"}, "lint": {"runs-on": "ubuntu-latest"}}})
	count(pull_request_runners.deny) == 0 with input as i
}

test_pull_request_target_is_held_to_the_same_rule if {
	count(refusals(with_workflow({"on": {"pull_request_target": null}, "jobs": {"plan": {"runs-on": pool}}}))) == 1
}

test_the_bare_on_key_read_as_true_is_still_read if {
	count(refusals(with_workflow({"true": {"pull_request": null}, "jobs": {"plan": {"runs-on": pool}}}))) == 1
}

test_the_management_pool_hidden_in_a_label_list if {
	some msg in refusals(on_pr({"runs-on": [pool, "linux"]}))
	contains(msg, "a list, a group or an expression")
}

test_a_runner_group_cannot_be_identified_so_it_is_refused if {
	count(refusals(on_pr({"runs-on": {"group": "estate"}}))) == 1
}

test_a_label_nobody_declared_is_a_runner_nobody_vetted if {
	count(refusals(on_pr({"runs-on": "some-new-pool"}))) == 1
}

test_an_expression_cannot_be_read_so_it_is_refused if {
	count(refusals(on_pr({"runs-on": "${{ inputs.runner }}"}))) == 1
}

test_the_nightly_form_a_pool_by_night_and_githubs_by_day if {
	i := with_workflow({"on": {"pull_request": null, "schedule": [{"cron": "0 3 * * *"}]}, "jobs": {"test": {"runs-on": "${{ github.event_name == 'schedule' && format('pr-builds-{0}', vars.pull_request_runners_site) || 'ubuntu-latest' }}"}}})
	count(pull_request_runners.deny) == 0 with input as i
}

test_the_nightly_form_with_the_estates_runner_by_day if {
	i := with_workflow({"on": {"pull_request": null, "schedule": [{"cron": "0 3 * * *"}]}, "jobs": {"test": {"runs-on": "${{ github.event_name == 'schedule' && format('pr-builds-{0}', vars.pull_request_runners_site) || 'homelab-site0' }}"}}})
	count(refusals(i)) == 1
}

test_a_workflow_no_pull_request_can_trigger_is_out_of_scope if {
	i := with_workflow({"on": {"schedule": [{"cron": "0 4 * * *"}]}, "jobs": {"test": {"runs-on": pool}}})
	count(refusals(i)) == 0 with input as i
	some msg in pull_request_runners.deny with input as i
	contains(msg, "no pull-request job was found")
}

test_too_few_workflows_is_refused if {
	some msg in pull_request_runners.deny with input as [{"path": ".github/workflows/a.yml", "contents": {"on": "pull_request", "jobs": {"j": {"runs-on": "ubuntu-latest"}}}}]
	contains(msg, "only 1 workflows were read")
}

test_no_declared_pool_is_refused if {
	some msg in pull_request_runners.deny with input as array.slice(on_pr({"runs-on": "ubuntu-latest"}), 1, 7)
	contains(msg, "declares no pull-request pool")
}

test_reachable_from_a_pull_request if {
	pull_request_runners.reachable("")
	not pull_request_runners.reachable("github.event_name == 'push'")
	pull_request_runners.reachable("github.event_name == 'pull_request'")
	not pull_request_runners.reachable("github.event_name != 'pull_request'")
	pull_request_runners.reachable("github.event_name == 'push' || github.event_name == 'pull_request'")
	pull_request_runners.reachable("github.event.pull_request.head.repo.full_name == github.repository")
	pull_request_runners.reachable("inputs.run_it == 'yes'")
}
