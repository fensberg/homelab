# The fabricator describes a version whether or not this run published it.
#
# The describing step is reached by a run that published nothing: it has no
# condition of its own, and the version it describes is the one this run
# numbered or, failing that, the one the reuse step found already published.
# The step's script is run as shipped by the fabricator's own tests; this is
# how the job hands it what it runs with, which no run of the script alone can
# see.
package rules.fabricator_describing

import data.lib
import rego.v1

steps := object.get(lib.workflows, [".github/workflows/fabricator.yml", "jobs", "package", "steps"], [])

ids[s.name] := object.get(s, "id", "") if {
	some s in steps
	s.name
}

describing := [s | some s in steps; s.name == "Describe the version"]

deny contains msg if {
	count(describing) == 0
	msg := "the fabricator's package job has no step that describes a version"
}

deny contains msg if {
	some s in describing
	s["if"]
	msg := sprintf("the describing step runs only if %q, so a version an earlier run published and could not describe is never described", [s["if"]])
}

deny contains msg if {
	some s in describing
	some from in ["Number the version", "Reuse a package of the same contents"]
	given := object.get(s, ["env", "VERSION"], "")
	not hands(given, object.get(ids, from, ""))
	msg := sprintf("the describing step is handed the version only by %q, and not by the step %q", [given, from])
}

hands(given, id) if {
	id != ""
	contains(given, sprintf("steps.%s.outputs.version", [id]))
}
