package rules.app_keys_test

import data.rules.app_keys
import rego.v1

patrol(steps) := {"path": ".github/workflows/security-patrol.yml", "contents": {"jobs": {"keys": {"permissions": {"contents": "read"}, "steps": steps}}}}

wf(path, values) := {"path": path, "contents": {"jobs": {"j": {"steps": [{"env": values}]}}}}

holders := [
	wf(".github/workflows/a.yml", {"KEY": "${{ secrets.ALPHA_BOT_PRIVATE_KEY }}"}),
	wf(".github/workflows/b.yml", {"KEY": "${{ secrets.BETA_BOT_PRIVATE_KEY }}", "OTHER": "${{ secrets.ALPHA_BOT_PRIVATE_KEY }}", "NOT_ONE": "${{ secrets.SOME_TOKEN }}"}),
]

proving(keys) := patrol([{"with": {"private-key": sprintf("${{ secrets.%s }}", [k])}} | some k in keys])

test_every_key_proved_passes if {
	count(app_keys.deny) == 0 with input as array.concat(holders, [proving(["ALPHA_BOT_PRIVATE_KEY", "BETA_BOT_PRIVATE_KEY"])])
}

test_a_key_the_patrol_does_not_prove_is_named_with_who_holds_it if {
	some msg in app_keys.deny with input as array.concat(holders, [proving(["ALPHA_BOT_PRIVATE_KEY"])])
	startswith(msg, "BETA_BOT_PRIVATE_KEY is handed to b.yml")
}

test_a_patrol_that_writes_is_refused if {
	writing := {"path": ".github/workflows/security-patrol.yml", "contents": {"jobs": {"keys": {"permissions": {"issues": "write"}, "steps": [{"with": {"k": "${{ secrets.ALPHA_BOT_PRIVATE_KEY }}"}}]}}}}
	some msg in app_keys.deny with input as [writing]
	contains(msg, "can write issues")
}

test_no_keys_job_is_refused if {
	some msg in app_keys.deny with input as holders
	contains(msg, "has no \"keys\" job")
}
