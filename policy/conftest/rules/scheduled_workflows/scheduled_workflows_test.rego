package rules.scheduled_workflows_test

import data.rules.scheduled_workflows
import rego.v1

target(file) := $"https://api.github.com/repos/o/r/actions/workflows/{file}/runs?event=schedule&status=completed&per_page=1"

wf(file, crons) := {"path": $".github/workflows/{file}", "contents": {"true": {"schedule": [{"cron": c} | some c in crons], "push": null}, "jobs": {}}}

scrape(files, interval) := {"path": "clusters/core/w.yaml", "contents": {"kind": "ScrapeConfig", "metadata": {"name": "scheduled-workflows"}, "spec": {"scrapeInterval": interval, "staticConfigs": [{"targets": [target(f) | some f in files]}]}}}

due(expr, asked_for) := {"alert": "ScheduledWorkflowHasNotRunWhenDue", "expr": expr, "labels": {"asked_for": asked_for}}

rules(list) := {"path": "clusters/core/w.yaml", "contents": {"kind": "PrometheusRule", "metadata": {"name": "scheduled-workflows"}, "spec": {"groups": [{"rules": list}]}}}

good_rules := rules([
	{"alert": "ScheduledWorkflowIsFailing", "expr": "x"},
	due(`changes(x{instance=~"hourly.yml"}[12h])`, "hourly"),
	due(`changes(x{instance=~"daily.yml|weekday.yml"}[30h])`, "daily"),
	due(`changes(x{instance=~"weekly.yml"}[9d])`, "weekly"),
])

workflows := [
	wf("hourly.yml", ["7 * * * *", "0 3 * * 1"]),
	wf("daily.yml", ["0 3 * * *"]),
	wf("weekday.yml", ["0 3 * * *"]),
	wf("weekly.yml", ["0 4 * * 1"]),
	{"path": ".github/workflows/push.yml", "contents": {"true": {"push": null}, "jobs": {}}},
]

scheduled := ["hourly.yml", "daily.yml", "weekday.yml", "weekly.yml"]

good := array.concat(workflows, [scrape(scheduled, "15m"), good_rules])

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_the_watch_as_it_should_be_passes if {
	count(scheduled_workflows.deny) == 0 with input as good
}

test_a_scheduled_workflow_not_asked_about_is_refused if {
	denies(scheduled_workflows.deny, "weekday.yml runs on a schedule and the cluster is not asked about it") with input as array.concat(workflows, [scrape(["hourly.yml", "daily.yml", "weekly.yml"], "15m"), good_rules])
}

test_an_address_for_no_scheduled_workflow_is_refused if {
	denies(scheduled_workflows.deny, "asks about push.yml, and no workflow of that name runs on a schedule") with input as array.concat(workflows, [scrape(array.concat(scheduled, ["push.yml"]), "15m"), good_rules])
}

test_any_other_question_is_refused if {
	s := {"path": "clusters/core/w.yaml", "contents": {"kind": "ScrapeConfig", "metadata": {"name": "scheduled-workflows"}, "spec": {"scrapeInterval": "15m", "staticConfigs": [{"targets": array.concat([target(f) | some f in scheduled], ["https://api.github.com/rate_limit"])}]}}}
	denies(scheduled_workflows.deny, "which is not a workflow's latest completed scheduled run") with input as array.concat(workflows, [s, good_rules])
}

test_no_watch_is_refused if {
	denies(scheduled_workflows.deny, "0 ScrapeConfig named scheduled-workflows") with input as array.concat(workflows, [good_rules])
}

test_no_scheduled_workflow_is_refused if {
	denies(scheduled_workflows.deny, "no workflow runs on a schedule") with input as [scrape([], "15m"), good_rules]
}
