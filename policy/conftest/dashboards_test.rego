package rules.dashboards_test

import data.rules.dashboards
import rego.v1

panel := {"title": "Pressure", "description": "What waits on the disk.", "targets": [{"expr": "rate(x[5m])"}]}

board(contents) := {"path": "clusters/core/c/dashboards/where.json", "contents": object.union({"uid": "where", "schemaVersion": 39, "panels": [panel]}, contents)}

listing(files, labels) := {"path": "clusters/core/c/kustomization.yaml", "contents": {"configMapGenerator": [{"name": "d", "files": files}], "generatorOptions": {"labels": labels}}}

listed := listing(["dashboards/where.json"], {"grafana_dashboard": "1"})

denies(i, words) if {
	some msg in dashboards.deny with input as i
	contains(msg, words)
}

test_a_dashboard_handed_over_and_explained_passes if {
	count(dashboards.deny) == 0 with input as [board({}), listed]
}

test_a_variable_is_refused if {
	denies([board({"panels": [object.union(panel, {"targets": [{"expr": "rate(x[$__rate_interval])"}]})]}), listed], `holds "$__rate_interval"`)
}

test_a_uid_not_the_files_name_is_refused if {
	denies([board({"uid": "Where the time goes"}), listed], `has the uid "Where the time goes"`)
}

test_no_panel_is_refused if {
	denies([board({"panels": []}), listed], "has no panel")
}

test_a_panel_with_no_title_is_refused if {
	denies([board({"panels": [object.remove(panel, ["title"])]}), listed], "a panel with no title")
}

test_a_panel_that_does_not_say_what_it_is_for_is_refused if {
	denies([board({"panels": [object.union(panel, {"description": " "})]}), listed], "does not say what it is for")
}

test_a_panel_that_asks_nothing_is_refused if {
	denies([board({"panels": [object.remove(panel, ["targets"])]}), listed], "asks Prometheus nothing")
}

test_a_dashboard_nothing_lists_is_refused if {
	denies([board({}), listing([], {"grafana_dashboard": "1"})], "a file and not a dashboard")
	denies([board({})], "no kustomization is beside its directory")
}

test_an_unlabelled_listing_is_refused if {
	denies([board({}), listing(["dashboards/where.json"], {})], "not labelled grafana_dashboard")
}

test_no_dashboard_is_refused if {
	denies([], "no dashboard was found")
}
