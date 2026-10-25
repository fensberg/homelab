package rules.dashboards_listed_test

import data.rules.dashboards_listed
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
	count(dashboards_listed.deny) == 0 with input as [board({}), listed]
}

test_a_dashboard_nothing_lists_is_refused if {
	denies(dashboards_listed.deny, "a file and not a dashboard") with input as [board({}), listing([], {"grafana_dashboard": "1"})]
	denies(dashboards_listed.deny, "no kustomization is beside its directory") with input as [board({})]
}

test_an_unlabelled_listing_is_refused if {
	denies(dashboards_listed.deny, "not labelled grafana_dashboard") with input as [board({}), listing(["dashboards/where.json"], {})]
}

test_no_dashboard_is_refused if {
	denies(dashboards_listed.deny, "no dashboard was found") with input as [listed]
}
