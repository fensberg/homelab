package rules.plan_heartbeat_cadence_test

import data.rules.plan_heartbeat_cadence
import rego.v1

stack(route) := {"path": "clusters/k.yaml", "contents": {"kind": "HelmRelease", "metadata": {"name": "kube-prometheus-stack"}, "spec": {"values": {"alertmanager": {"config": {"route": {"receiver": "null", "routes": [{"receiver": "other"}, route]}}}}}}}

schedule(cron) := {"path": "estate.plan.json", "contents": {"planned_values": {"root_module": {"child_modules": [{"resources": [{"address": "module.watchman.cloudflare_workers_cron_trigger.watchman", "type": "cloudflare_workers_cron_trigger", "values": {"schedules": [{"cron": cron}]}}]}]}}}}

watchman(n) := schedule(sprintf("*/%d * * * *", [n]))

route(group, repeat) := {"receiver": "watchman", "group_interval": group, "repeat_interval": repeat}

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_a_repeat_just_short_of_two_looks_rings_on_the_second if {
	count(plan_heartbeat_cadence.deny) == 0 with input as [stack(route("5m", "9m")), watchman(10)]
}

test_a_repeat_of_exactly_two_looks_is_refused if {
	denies(plan_heartbeat_cadence.deny, "waits a whole look more") with input as [stack(route("5m", "10m")), watchman(10)]
}

test_ringing_less_often_than_the_watchman_looks_is_refused if {
	denies(plan_heartbeat_cadence.deny, "rings the watchman every 30 minutes") with input as [stack(route("5m", "30m")), watchman(10)]
	denies(plan_heartbeat_cadence.deny, "looks round every 10") with input as [stack(route("5m", "30m")), watchman(10)]
}

test_an_interval_not_in_minutes_is_refused if {
	denies(plan_heartbeat_cadence.deny, "which is not a number of minutes") with input as [stack(route("5m", "1h")), watchman(10)]
}

test_no_route_to_the_watchman_is_refused if {
	denies(plan_heartbeat_cadence.deny, "sends nothing to the watchman") with input as [stack({"receiver": "other"}), watchman(10)]
}

test_a_schedule_not_of_every_so_many_minutes_is_refused if {
	denies(plan_heartbeat_cadence.deny, "which is not looked round every so many minutes") with input as [stack(route("5m", "9m")), schedule("0 * * * *")]
}

test_no_schedule_is_refused if {
	denies(plan_heartbeat_cadence.deny, "is looked round on 0 schedules") with input as [stack(route("5m", "9m"))]
}
