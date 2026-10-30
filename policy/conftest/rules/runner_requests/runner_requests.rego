# A runner with no requests is BestEffort, which is the first cgroup the OOM
# controller reaches for - it is what killed the integration run in #234. The
# requests are also what the scheduler reserves, so a build lands on a node
# that can hold it rather than finding out at minute twelve.
#
# Deliberately no assertion that a limit is absent. That is a judgement this
# rule should not freeze: #234 argues against a memory limit because capping a
# build turns an infrastructure shortfall into a red test, but that reasoning
# belongs in the record and in review, not in a check that would fail the day
# somebody has a good reason.
package rules.runner_requests

import data.lib
import rego.v1

sets := [f | some f in lib.objects; f.contents.kind == "HelmRelease"; f.contents.spec.chart.spec.chart == "gha-runner-scale-set"]

containers(f) := object.get(f.contents, ["spec", "values", "template", "spec", "containers"], [])

deny contains msg if {
	count(sets) == 0
	msg := "no runner scale set was found, so what its pods ask for cannot be read"
}

deny contains msg if {
	some f in sets
	count(containers(f)) == 0
	msg := $"the runner scale set {f.contents.metadata.name} declares no containers, so this rule is asserting nothing"
}

deny contains msg if {
	some f in sets
	some c in containers(f)
	some want in ["cpu", "memory"]
	object.get(c, ["resources", "requests", want], "") == ""
	msg := $"{f.contents.metadata.name}: container \"{c.name}\" requests no {want}, so it is BestEffort and the OOM controller will choose it first (#234)"
}
