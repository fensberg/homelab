# The coverage shim dies with the thing that needs it.
#
# tests/coverage-baseline.json carries a bare "go" key that is a floor for
# nothing. It exists because .github/workflows/pr-validation.yml still runs its
# own coverage step against scripts/contractor alone and asks the gate for that
# key, and the agent cannot edit a workflow - the change is handed over as a
# patch instead.
#
# A shim that outlives its reason is indistinguishable from a floor somebody
# chose, and this repository has been bitten by exactly that: a baseline sat
# ten points below the measured figure for long enough that nobody remembered
# setting it. So the two are tied together here rather than left to whoever
# applies the patch to notice.
package rules.coverage_shim

import data.lib
import rego.v1

workflow := [f | some f in lib.at(".github/workflows/pr-validation.yml")]

baseline := [f | some f in lib.at("tests/coverage-baseline.json")]

asks_for_it if {
	some f in workflow
	walk(f.contents, [_, v])
	is_string(v)
	contains(v, `coverage-gate.sh go "`)
}

shim_present if {
	some f in baseline
	"go" in object.keys(object.get(f.contents, "baselines", {}))
}

deny contains msg if {
	count(workflow) + count(baseline) != 2
	msg := "pr-validation.yml and tests/coverage-baseline.json were not both read, so whether the shim outlives its reason is not known"
}

deny contains msg if {
	asks_for_it
	not shim_present
	msg := `pr-validation.yml asks the coverage gate for the key "go" and tests/coverage-baseline.json does not declare one, so the Test lane fails on every pull request. Either restore the shim or apply the patch that stops the workflow needing it.`
}

deny contains msg if {
	not asks_for_it
	shim_present
	msg := `the workflow no longer asks for the bare "go" key, so the shim in tests/coverage-baseline.json is a floor for nothing. Remove it: a leftover number is indistinguishable from one somebody chose, which is how a baseline once sat ten points below the real figure unnoticed.`
}
