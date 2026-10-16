package main_test

import data.main
import rego.v1

test_a_rules_refusal_is_collected_and_named if {
	some msg in main.deny with data.rules.planted.deny as {"it refuses"}
	msg == "planted: it refuses"
}

test_a_test_package_is_not_collected if {
	count(main.deny) == 0 with data.rules as {"planted_test": {"deny": {"x"}}}
}
