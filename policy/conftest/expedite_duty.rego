# The bypass belongs to the expediting duty, not to procurement.
#
# Procurement is one program with several duties. Expediting is the only one
# that holds elevated permission - its App may merge its own delivery without
# a review, bounded by `superintendent enforce-standing-order`. Delivering
# holds the same credential to open a pull request moving a release pin, and
# never merges one: the standing order refuses a delivery under -bypass.
# Ordering writes scripts/deliveries.lock and holds nothing; its output is
# committed through an ordinary review.
#
# So the credential must never run a verb outside its duty. A job that mints
# it and then runs `procurement order` would write the lock with a token that
# can merge without review - a supply-chain decision taken under a standing
# order that covers one digest. And ordering needs no workflow at all: it is
# run by a person, and read by security on every commit.
#
# The workflows holding the credential are FOUND, by the secret they read,
# rather than named here, so renaming the workflow or adding a second one
# cannot step outside this.
package rules.expedite_duty

import data.lib
import rego.v1

expedite_secret := `secrets\.PROCUREMENT_BOT_[A-Z_]+`

procurement_verb := `(?:go run -C scripts/procurement \.|toolshed/procurement)\s+([a-z][a-z0-9-]*)`

strings_of(doc) := [v | walk(doc, [_, v]); is_string(v)]

holders contains file if {
	some file, doc in lib.workflows
	some v in strings_of(doc)
	regex.match(expedite_secret, v)
}

verbs contains [file, m[1]] if {
	some file, doc in lib.workflows
	some v in strings_of(doc)
	some m in regex.find_all_string_submatch_n(procurement_verb, v, -1)
}

deny contains msg if {
	some [file, "order"] in verbs
	msg := sprintf("%s runs `procurement order`. Ordering writes scripts/deliveries.lock, which decides what every lane and workstation installs. It is run by a person and committed through review; no workflow orders, and above all not one that could hold the expedite credential.", [file])
}

delivers(verb) if verb == "deliver"

delivers(verb) if startswith(verb, "deliver-")

deny contains msg if {
	some [file, verb] in verbs
	delivers(verb)
	not file in holders
	msg := sprintf("%s runs `procurement deliver` and holds no procurement credential, so the pull request it would open has nothing to open it with", [file])
}

deny contains msg if {
	some [file, verb] in verbs
	verb != "order"
	not delivers(verb)
	not startswith(verb, "expedite-")
	file in holders
	msg := sprintf("%s holds the expedite credential and runs `procurement %s`, which is not an expedite-* verb. The bypass belongs to the expediting duty, not to procurement. Run it somewhere that holds no such credential.", [file, verb])
}

deny contains msg if {
	some [file, verb] in verbs
	startswith(verb, "expedite-")
	not file in holders
	msg := sprintf("%s runs `procurement %s` and holds no expedite credential; expediting runs in the workflow that does, so the duty and its bounds stay in one place", [file, verb])
}

deny contains msg if {
	count(holders) == 0
	msg := "no workflow reads a PROCUREMENT_BOT_ secret, so this cannot tell which one holds the credential - the secret was renamed, and this rule is asserting nothing"
}

deny contains msg if {
	count({v | some [_, v] in verbs; startswith(v, "expedite-")}) == 0
	msg := "no workflow runs a procurement expedite-* verb, so the pattern that finds procurement's verbs has stopped matching and nothing was checked"
}
