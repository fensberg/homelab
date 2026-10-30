# Every workflow defaults to a pipefail shell. Naming bash gets
# `bash --noprofile --norc -eo pipefail {0}`; GitHub's default is `bash -e
# {0}`, where a failing command in a pipeline is hidden by what it pipes into.
package rules.workflow_pipefail

import data.lib
import rego.v1

# A brace of a template a message quotes, which a template string cannot
# hold as it is.
open := "{"

close := "}"

deny contains msg if {
	count(lib.workflows) == 0
	msg := "found no workflows, so no workflow's shell was checked"
}

deny contains msg if {
	some file, doc in lib.workflows
	object.get(object.get(object.get(doc, "defaults", {}), "run", {}), "shell", "") != "bash"
	msg := $"{file} does not set a pipefail shell. Add defaults: {open}run: {open}shell: bash{close}{close} above its jobs; without it every run: step runs under `bash -e {open}0{close}`, where a failing command in a pipeline is hidden by whatever it pipes into."
}
