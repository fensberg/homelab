package main_test

import data.main
import rego.v1

# The rules are replaced whole: patching in one package beside the real ones
# made the collector evaluate every rule in the repository too, a second or
# two that a test on a busy machine runs past its time limit in.
test_a_rules_refusal_is_collected_and_named if {
	some msg in main.deny with data.rules as {"planted": {"deny": {"it refuses"}}}
	msg == "planted: it refuses"
}

test_a_test_package_is_not_collected if {
	count(main.deny) == 0 with data.rules as {"planted_test": {"deny": {"x"}}}
}

test_a_rules_warning_is_collected_and_named if {
	some msg in main.warn with data.rules as {"planted": {"warn": {"it warns"}}}
	msg == "planted: it warns"
}

test_a_test_packages_warning_is_not_collected if {
	count(main.warn) == 0 with data.rules as {"planted_test": {"warn": {"x"}}}
}
