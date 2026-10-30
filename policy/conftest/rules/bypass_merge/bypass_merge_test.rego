package rules.bypass_merge_test

import data.rules.bypass_merge
import rego.v1

wf(steps) := {"path": ".github/workflows/w.yml", "contents": {"jobs": {"release": {"steps": steps}}}}

judge := {"run": "go run -C scripts/superintendent . enforce-standing-order -head \"$HEAD\""}

test_a_judged_merge_of_the_judged_head_passes if {
	count(bypass_merge.deny) == 0 with input as [wf([judge, {"run": "gh pr merge \"$PR\" --squash --admin \\\n  --match-head-commit \"$HEAD\""}])]
}

test_a_merge_that_rides_along_is_refused if {
	some msg in bypass_merge.deny with input as [wf([judge, {"run": "gh pr merge \"$PR\" --squash --admin"}])]
	contains(msg, "without --match-head-commit")
}

test_an_unjudged_merge_is_refused if {
	some msg in bypass_merge.deny with input as [wf([{"run": "gh pr merge \"$PR\" --admin --match-head-commit \"$HEAD\""}])]
	contains(msg, "never runs enforce-standing-order")
}

test_no_bypass_merge_at_all_is_refused if {
	some msg in bypass_merge.deny with input as [wf([{"run": "echo nothing"}])]
	contains(msg, "checked nothing")
}
