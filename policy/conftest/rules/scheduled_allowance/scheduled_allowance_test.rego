package rules.scheduled_allowance_test

import data.rules.scheduled_allowance
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
	count(scheduled_allowance.deny) == 0 with input as good
}

test_asking_too_often_is_refused if {
	denies(scheduled_allowance.deny, "asks GitHub 48 times an hour: 4 workflows every 5 minutes. A caller with no token is allowed 60") with input as array.concat(workflows, [scrape(scheduled, "5m"), good_rules])
}

test_an_interval_not_in_minutes_is_refused if {
	denies(scheduled_allowance.deny, "which is not a number of minutes") with input as array.concat(workflows, [scrape(scheduled, "1h"), good_rules])
}

test_no_watch_is_refused if {
	denies(scheduled_allowance.deny, "0 ScrapeConfig named scheduled-workflows") with input as workflows
}
