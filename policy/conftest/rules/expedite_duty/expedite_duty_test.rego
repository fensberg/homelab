package rules.expedite_duty_test

import data.rules.expedite_duty
import rego.v1

wf(name, key, runs) := {"path": $".github/workflows/{name}.yml", "contents": {"jobs": {"j": {"steps": array.concat([{"env": {"KEY": key}}], [{"run": r} | some r in runs])}}}}

expedite := wf("expedite", "${{ secrets.PROCUREMENT_BOT_PRIVATE_KEY }}", ["go run -C scripts/procurement . expedite-renewal", "./toolshed/procurement deliver-pin -x"])

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_the_duty_under_its_credential_passes if {
	count(expedite_duty.deny) == 0 with input as [expedite, wf("other", "${{ secrets.OTHER }}", ["go run -C scripts/procurement . reconcile"])]
}

test_ordering_anywhere_is_refused if {
	denies(expedite_duty.deny, "runs `procurement order`") with input as [expedite, wf("o", "", ["go run -C scripts/procurement . order"])]
}

test_another_verb_under_the_credential_is_refused if {
	denies(expedite_duty.deny, "which is not an expedite-* verb") with input as [expedite, wf("a", "${{ secrets.PROCUREMENT_BOT_CLIENT_ID }}", ["go run -C scripts/procurement . reconcile-suppliers"])]
}

test_expediting_without_the_credential_is_refused if {
	denies(expedite_duty.deny, "holds no expedite credential") with input as [expedite, wf("b", "", ["go run -C scripts/procurement . expedite-x"])]
}

test_delivering_without_the_credential_is_refused if {
	denies(expedite_duty.deny, "holds no procurement credential") with input as [expedite, wf("c", "", ["toolshed/procurement deliver"])]
}

test_nothing_holding_the_credential_is_refused if {
	denies(expedite_duty.deny, "no workflow reads a PROCUREMENT_BOT_ secret") with input as [wf("d", "", ["go run -C scripts/procurement . expedite-x"])]
}

test_no_expediting_is_refused if {
	denies(expedite_duty.deny, "no workflow runs a procurement expedite-* verb") with input as [wf("e", "${{ secrets.PROCUREMENT_BOT_X }}", [])]
}
