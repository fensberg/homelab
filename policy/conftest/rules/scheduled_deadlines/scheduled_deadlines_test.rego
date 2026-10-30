package rules.scheduled_deadlines_test

import data.rules.scheduled_deadlines
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
	count(scheduled_deadlines.deny) == 0 with input as good
}

test_the_most_often_cron_decides if {
	scheduled_deadlines.asked_for["hourly.yml"] == "hourly" with input as good
	scheduled_deadlines.asked_for["weekly.yml"] == "weekly" with input as good
}

test_a_cron_not_of_five_fields_is_refused if {
	denies(scheduled_deadlines.deny, "is scheduled by \"@daily\", which is not five fields") with input as array.concat(good, [wf("odd.yml", ["@daily"])])
}

test_a_workflow_with_no_deadline_is_refused if {
	r := rules([due(`x{instance=~"daily.yml|weekday.yml"}`, "daily"), due(`x{instance=~"weekly.yml"}`, "weekly")])
	denies(scheduled_deadlines.deny, "hourly.yml is asked for hourly and has no deadline") with input as array.concat(workflows, [scrape(scheduled, "15m"), r])
}

test_the_wrong_deadline_is_refused if {
	r := rules([due(`x{instance=~"daily.yml|weekday.yml|hourly.yml"}`, "daily"), due(`x{instance=~"weekly.yml"}`, "weekly")])
	denies(scheduled_deadlines.deny, "either never late or always late") with input as array.concat(workflows, [scrape(scheduled, "15m"), r])
}

test_two_deadlines_are_refused if {
	r := rules([due(`x{instance=~"hourly.yml"}`, "hourly"), due(`x{instance=~"daily.yml|weekday.yml|hourly.yml"}`, "daily"), due(`x{instance=~"weekly.yml"}`, "weekly")])
	denies(scheduled_deadlines.deny, "hourly.yml is given 2 deadlines") with input as array.concat(workflows, [scrape(scheduled, "15m"), r])
}

test_an_alert_naming_no_workflow_is_refused if {
	r := rules([due(`x{instance=~"hourly.yml"}`, "hourly"), due(`x{instance=~"daily.yml|weekday.yml"}`, "daily"), due(`x{instance=~"weekly.yml"}`, "weekly"), due("x", "daily")])
	denies(scheduled_deadlines.deny, "names no workflow") with input as array.concat(workflows, [scrape(scheduled, "15m"), r])
}

test_a_deadline_for_no_scheduled_workflow_is_refused if {
	r := rules([due(`x{instance=~"hourly.yml"}`, "hourly"), due(`x{instance=~"daily.yml|weekday.yml|gone.yml"}`, "daily"), due(`x{instance=~"weekly.yml"}`, "weekly")])
	denies(scheduled_deadlines.deny, "gives gone.yml a deadline") with input as array.concat(workflows, [scrape(scheduled, "15m"), r])
}

test_no_scheduled_workflow_is_refused if {
	denies(scheduled_deadlines.deny, "no workflow was found with a schedule") with input as [good_rules]
}
