package rules.heartbeat_cadence_test

import data.rules.heartbeat_cadence
import rego.v1

stack(route) := {"path": "clusters/k.yaml", "contents": {"kind": "HelmRelease", "metadata": {"name": "kube-prometheus-stack"}, "spec": {"values": {"alertmanager": {"config": {"route": {"receiver": "null", "routes": [{"receiver": "other"}, route]}}}}}}}

watchman(n) := {"path": "management/estate/watchman/watchman.tf", "contents": {"locals": [{"looks_every_minutes": n}]}}

route(group, repeat) := {"receiver": "watchman", "group_interval": group, "repeat_interval": repeat}

denies(i, words) if {
	some msg in heartbeat_cadence.deny with input as i
	contains(msg, words)
}

test_a_repeat_just_short_of_two_looks_rings_on_the_second if {
	count(heartbeat_cadence.deny) == 0 with input as [stack(route("5m", "9m")), watchman(10)]
}

test_a_repeat_of_exactly_two_looks_is_refused if {
	denies([stack(route("5m", "10m")), watchman(10)], "waits a whole look more")
}

test_ringing_less_often_than_the_watchman_looks_is_refused if {
	denies([stack(route("5m", "30m")), watchman(10)], "rings the watchman every 30 minutes")
	denies([stack(route("5m", "30m")), watchman(10)], "looks round every 10")
}

test_an_interval_not_in_minutes_is_refused if {
	denies([stack(route("5m", "1h")), watchman(10)], "which is not a number of minutes")
}

test_no_route_to_the_watchman_is_refused if {
	denies([stack({"receiver": "other"}), watchman(10)], "sends nothing to the watchman")
}

test_a_cadence_that_is_not_a_number_is_refused if {
	denies([stack(route("5m", "9m")), watchman("${var.x}")], "not a plain number of minutes")
}

test_no_cadence_is_refused if {
	denies([stack(route("5m", "9m"))], "0 places say how often")
}
