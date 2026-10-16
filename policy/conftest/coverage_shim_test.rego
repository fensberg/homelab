package rules.coverage_shim_test

import data.rules.coverage_shim
import rego.v1

files(run, baselines) := [
	{"path": ".github/workflows/pr-validation.yml", "contents": {"jobs": {"test": {"steps": [{"run": run}]}}}},
	{"path": "tests/coverage-baseline.json", "contents": {"baselines": baselines}},
]

test_neither_passes if {
	count(coverage_shim.deny) == 0 with input as files("go test ./...", {"go/clerk": 60})
}

test_both_pass if {
	count(coverage_shim.deny) == 0 with input as files(`tests/coverage-gate.sh go "$pct"`, {"go": 34, "go/clerk": 60})
}

test_asked_for_and_missing_is_refused if {
	some msg in coverage_shim.deny with input as files(`tests/coverage-gate.sh go "$pct"`, {"go/clerk": 60})
	contains(msg, "the Test lane fails on every pull request")
}

test_a_shim_for_nothing_is_refused if {
	some msg in coverage_shim.deny with input as files("go test ./...", {"go": 34, "go/clerk": 60})
	contains(msg, "floor for nothing")
}

test_nothing_read_is_refused if {
	some msg in coverage_shim.deny with input as []
	contains(msg, "were not both read")
}
