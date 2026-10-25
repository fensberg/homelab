package rules.dashboards_test

import data.rules.dashboards
import rego.v1

panel := {"title": "Pressure", "description": "What waits on the disk.", "targets": [{"expr": "rate(x[5m])"}]}

board(contents) := {"path": "clusters/core/c/dashboards/where.json", "contents": object.union({"uid": "where", "schemaVersion": 39, "panels": [panel]}, contents)}

listing(files, labels) := {"path": "clusters/core/c/kustomization.yaml", "contents": {"configMapGenerator": [{"name": "d", "files": files}], "generatorOptions": {"labels": labels}}}

listed := listing(["dashboards/where.json"], {"grafana_dashboard": "1"})

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_a_dashboard_handed_over_and_explained_passes if {
	count(dashboards.deny) == 0 with input as [board({}), listed]
}

test_a_variable_is_refused if {
	denies(dashboards.deny, `holds "$__rate_interval"`) with input as [board({"panels": [object.union(panel, {"targets": [{"expr": "rate(x[$__rate_interval])"}]})]}), listed]
}

test_a_uid_not_the_files_name_is_refused if {
	denies(dashboards.deny, `has the uid "Where the time goes"`) with input as [board({"uid": "Where the time goes"}), listed]
}

test_no_panel_is_refused if {
	denies(dashboards.deny, "has no panel") with input as [board({"panels": []}), listed]
}

test_a_panel_with_no_title_is_refused if {
	denies(dashboards.deny, "a panel with no title") with input as [board({"panels": [object.remove(panel, ["title"])]}), listed]
}

test_a_panel_that_does_not_say_what_it_is_for_is_refused if {
	denies(dashboards.deny, "does not say what it is for") with input as [board({"panels": [object.union(panel, {"description": " "})]}), listed]
}

test_a_panel_that_asks_nothing_is_refused if {
	denies(dashboards.deny, "asks Prometheus nothing") with input as [board({"panels": [object.remove(panel, ["targets"])]}), listed]
}

test_a_dashboard_nothing_lists_is_refused if {
	denies(dashboards.deny, "a file and not a dashboard") with input as [board({}), listing([], {"grafana_dashboard": "1"})]
	denies(dashboards.deny, "no kustomization is beside its directory") with input as [board({})]
}

test_an_unlabelled_listing_is_refused if {
	denies(dashboards.deny, "not labelled grafana_dashboard") with input as [board({}), listing(["dashboards/where.json"], {})]
}

test_no_dashboard_is_refused if {
	denies(dashboards.deny, "no dashboard was found") with input as []
}
