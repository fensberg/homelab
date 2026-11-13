package rules.secrets_in_environments_test

import data.rules.secrets_in_environments as rule
import rego.v1

wf(contents) := [{"path": ".github/workflows/w.yml", "contents": contents}]

job(fields) := wf({"jobs": {"j": fields}})

reading(value) := {"steps": [{"env": {"V": value}}]}

test_a_job_reading_a_secret_through_its_environment_passes if {
	count(rule.deny) == 0 with input as job(object.union(reading("${{ secrets.KEY }}"), {"environment": "estate"}))
}

test_the_runs_own_token_needs_no_environment if {
	count(rule.deny) == 0 with input as job(reading("${{ secrets.GITHUB_TOKEN }}"))
}

test_a_job_reading_a_secret_with_no_environment_is_refused_by_name if {
	some msg in rule.deny with input as job(reading("${{ secrets.KEY }}"))
	startswith(msg, "job j in .github/workflows/w.yml reads KEY")
}

test_every_spelling_is_a_read if {
	some value in [
		"${{ secrets.KEY }}",
		"${{secrets . KEY}}",
		"prefix ${{ format('{0}', secrets.KEY) }} suffix",
		"${{ github.token }} and ${{ secrets.KEY }}",
	]
	some msg in rule.deny with input as job(reading(value))
	contains(msg, "reads KEY")
}

test_a_read_it_cannot_name_is_refused_as_one if {
	some msg in rule.deny with input as job(reading("${{ secrets[format('{0}', 'KEY')] }}"))
	contains(msg, "a name this cannot read")
}

test_an_if_is_an_expression if {
	some msg in rule.deny with input as job({"if": "secrets.KEY != ''", "steps": []})
	contains(msg, "reads KEY")
}

test_the_word_outside_an_expression_is_not_a_read if {
	count(rule.deny) == 0 with input as job({"steps": [{"run": "echo secrets.KEY lives in the vault"}]})
}

test_a_secret_read_outside_any_job_is_refused if {
	some msg in rule.deny with input as wf({"env": {"V": "${{ secrets.KEY }}"}, "jobs": {"j": {"environment": "estate", "steps": []}}})
	contains(msg, "reads KEY outside any job")
}

test_handing_every_secret_on_is_refused if {
	some msg in rule.deny with input as job({"uses": "./.github/workflows/other.yml", "secrets": "inherit", "environment": "estate"})
	contains(msg, "every secret it can reach")
}

test_no_workflow_is_refused if {
	some msg in rule.deny with input as [{"path": "a.yml", "contents": {}}]
	contains(msg, "no workflow was read")
}
