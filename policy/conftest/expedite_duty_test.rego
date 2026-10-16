package expedite_duty_test

import data.expedite_duty
import rego.v1

wf(name, key, runs) := {"path": sprintf(".github/workflows/%s.yml", [name]), "contents": {"jobs": {"j": {"steps": array.concat([{"env": {"KEY": key}}], [{"run": r} | some r in runs])}}}}

expedite := wf("expedite", "${{ secrets.PROCUREMENT_BOT_PRIVATE_KEY }}", ["go run -C scripts/procurement . expedite-renewal", "./toolshed/procurement deliver-pin -x"])

denies(i, words) if {
	some msg in expedite_duty.deny with input as i
	contains(msg, words)
}

test_the_duty_under_its_credential_passes if {
	count(expedite_duty.deny) == 0 with input as [expedite, wf("other", "${{ secrets.OTHER }}", ["go run -C scripts/procurement . reconcile"])]
}

test_ordering_anywhere_is_refused if {
	denies([expedite, wf("o", "", ["go run -C scripts/procurement . order"])], "runs `procurement order`")
}

test_another_verb_under_the_credential_is_refused if {
	denies([expedite, wf("a", "${{ secrets.PROCUREMENT_BOT_CLIENT_ID }}", ["go run -C scripts/procurement . reconcile-suppliers"])], "which is not an expedite-* verb")
}

test_expediting_without_the_credential_is_refused if {
	denies([expedite, wf("b", "", ["go run -C scripts/procurement . expedite-x"])], "holds no expedite credential")
}

test_delivering_without_the_credential_is_refused if {
	denies([expedite, wf("c", "", ["toolshed/procurement deliver"])], "holds no procurement credential")
}

test_nothing_holding_the_credential_is_refused if {
	denies([wf("d", "", ["go run -C scripts/procurement . expedite-x"])], "no workflow reads a PROCUREMENT_BOT_ secret")
}

test_no_expediting_is_refused if {
	denies([wf("e", "${{ secrets.PROCUREMENT_BOT_X }}", [])], "no workflow runs a procurement expedite-* verb")
}
