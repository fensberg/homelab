package scheduled_workflows_test

import data.scheduled_workflows
import rego.v1

target(file) := sprintf("https://api.github.com/repos/o/r/actions/workflows/%s/runs?event=schedule&status=completed&per_page=1", [file])

wf(file, crons) := {"path": sprintf(".github/workflows/%s", [file]), "contents": {"true": {"schedule": [{"cron": c} | some c in crons], "push": null}, "jobs": {}}}

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

all := ["hourly.yml", "daily.yml", "weekday.yml", "weekly.yml"]

good := array.concat(workflows, [scrape(all, "15m"), good_rules])

denies(i, words) if {
	some msg in scheduled_workflows.deny with input as i
	contains(msg, words)
}

test_the_watch_as_it_should_be_passes if {
	count(scheduled_workflows.deny) == 0 with input as good
}

test_the_most_often_cron_decides if {
	scheduled_workflows.asked_for["hourly.yml"] == "hourly" with input as good
	scheduled_workflows.asked_for["weekly.yml"] == "weekly" with input as good
}

test_a_scheduled_workflow_not_asked_about_is_refused if {
	denies(array.concat(workflows, [scrape(["hourly.yml", "daily.yml", "weekly.yml"], "15m"), good_rules]), "weekday.yml runs on a schedule and the cluster is not asked about it")
}

test_an_address_for_no_scheduled_workflow_is_refused if {
	denies(array.concat(workflows, [scrape(array.concat(all, ["push.yml"]), "15m"), good_rules]), "asks about push.yml, and no workflow of that name runs on a schedule")
}

test_any_other_question_is_refused if {
	s := {"path": "clusters/core/w.yaml", "contents": {"kind": "ScrapeConfig", "metadata": {"name": "scheduled-workflows"}, "spec": {"scrapeInterval": "15m", "staticConfigs": [{"targets": array.concat([target(f) | some f in all], ["https://api.github.com/rate_limit"])}]}}}
	denies(array.concat(workflows, [s, good_rules]), "which is not a workflow's latest completed scheduled run")
}

test_asking_too_often_is_refused if {
	denies(array.concat(workflows, [scrape(all, "5m"), good_rules]), "asks GitHub 48 times an hour: 4 workflows every 5 minutes. A caller with no token is allowed 60")
}

test_an_interval_not_in_minutes_is_refused if {
	denies(array.concat(workflows, [scrape(all, "1h"), good_rules]), "which is not a number of minutes")
}

test_no_watch_is_refused if {
	denies(array.concat(workflows, [good_rules]), "0 ScrapeConfig named scheduled-workflows")
}

test_no_scheduled_workflow_is_refused if {
	denies([scrape([], "15m"), good_rules], "no workflow runs on a schedule")
}

test_a_cron_not_of_five_fields_is_refused if {
	denies(array.concat(good, [wf("odd.yml", ["@daily"])]), "is scheduled by \"@daily\", which is not five fields")
}

test_a_workflow_with_no_deadline_is_refused if {
	r := rules([due(`x{instance=~"daily.yml|weekday.yml"}`, "daily"), due(`x{instance=~"weekly.yml"}`, "weekly")])
	denies(array.concat(workflows, [scrape(all, "15m"), r]), "hourly.yml is asked for hourly and has no deadline")
}

test_the_wrong_deadline_is_refused if {
	r := rules([due(`x{instance=~"daily.yml|weekday.yml|hourly.yml"}`, "daily"), due(`x{instance=~"weekly.yml"}`, "weekly")])
	denies(array.concat(workflows, [scrape(all, "15m"), r]), "either never late or always late")
}

test_two_deadlines_are_refused if {
	r := rules([due(`x{instance=~"hourly.yml"}`, "hourly"), due(`x{instance=~"daily.yml|weekday.yml|hourly.yml"}`, "daily"), due(`x{instance=~"weekly.yml"}`, "weekly")])
	denies(array.concat(workflows, [scrape(all, "15m"), r]), "hourly.yml is given 2 deadlines")
}

test_an_alert_naming_no_workflow_is_refused if {
	r := rules([due(`x{instance=~"hourly.yml"}`, "hourly"), due(`x{instance=~"daily.yml|weekday.yml"}`, "daily"), due(`x{instance=~"weekly.yml"}`, "weekly"), due("x", "daily")])
	denies(array.concat(workflows, [scrape(all, "15m"), r]), "names no workflow")
}

test_a_deadline_for_no_scheduled_workflow_is_refused if {
	r := rules([due(`x{instance=~"hourly.yml"}`, "hourly"), due(`x{instance=~"daily.yml|weekday.yml|gone.yml"}`, "daily"), due(`x{instance=~"weekly.yml"}`, "weekly")])
	denies(array.concat(workflows, [scrape(all, "15m"), r]), "gives gone.yml a deadline")
}
