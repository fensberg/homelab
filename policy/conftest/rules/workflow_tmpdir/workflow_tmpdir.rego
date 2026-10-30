# No step reads $TMPDIR. GitHub-hosted runners do not set it, so under set -u
# a step fails before doing anything (#440). $RUNNER_TEMP is set everywhere.
package rules.workflow_tmpdir

import data.lib
import rego.v1

deny contains msg if {
	count(lib.workflows) == 0
	msg := "found no workflows, so none was read for $TMPDIR"
}

deny contains msg if {
	some file, doc in lib.workflows
	walk(doc, [where, v])
	is_string(v)
	some line in split(v, "\n")
	not startswith(trim_space(line), "#")
	regex.match(`\$\{?TMPDIR\b`, line)
	msg := $"{file} reads $TMPDIR at {concat(".", [$"{p}" | some p in where])}. GitHub-hosted runners do not set TMPDIR, so under `set -u` this is \"TMPDIR: unbound variable\" and the step fails before doing anything (#440). Use $RUNNER_TEMP, which Actions sets on every runner."
}
