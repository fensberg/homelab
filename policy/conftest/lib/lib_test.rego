package lib_test

import data.lib
import rego.v1

# What the rules read lib's selectors for is tested with each rule; these
# are the forms of a file no rule's tests happen to hand it.

test_a_yaml_file_is_yaml_whichever_way_its_name_is_spelt if {
	lib.is_yml("a/b.yml")
	lib.is_yml("a/b.yaml")
	not lib.is_yml("a/b.json")
}

test_triggers_are_read_in_every_form_github_takes if {
	lib.triggers({"on": "push"}) == {"push"}
	lib.triggers({"on": ["push", "pull_request"]}) == {"push", "pull_request"}
	lib.triggers({"true": {"schedule": []}}) == {"schedule"}
	lib.triggers({}) == set()
}

test_an_actions_steps_are_read_with_their_environment if {
	action := {"path": ".github/actions/a/action.yml", "contents": {"env": {"A": "1"}, "runs": {"using": "composite", "steps": [{"run": "x", "env": {"B": "2"}}]}}}
	some [file, where, step, envs] in lib.steps_with_env with input as [action]
	file == ".github/actions/a/action.yml"
	where == ""
	step.run == "x"
	envs == [{"A": "1"}, {"B": "2"}]
}

test_a_plans_scope_is_the_directory_the_harness_wrote_it_under if {
	lib.plan_scope("/tmp/x/site/site0-cluster.plan.json") == "site"
	lib.plan_scope("/tmp/x/estate/estate.plan.json") == "estate"
	not lib.plan_scope("/tmp/x/elsewhere/a.plan.json")
	not lib.plan_scope("a.plan.json")
}
