# A step OpenTofu runs as a script is run by /bin/sh unless it names
# something else, and which sh that is depends on the machine.
#
# The step that applies the network manifest is bash: its first line sets
# `pipefail`. It named no interpreter, so it worked from a workstation whose
# sh takes that option, and on the estate's own runner it stopped within
# milliseconds of starting, before it had asked the cluster anything. A
# change to the network could then be delivered by hand and never by a
# merge, and nothing said so until a merge tried (2026-10-11).
package rules.scripted_steps

import data.lib
import rego.v1

steps contains [f.path, script] if {
	some f in lib.opentofu
	walk(f.contents, [path, script])
	count(path) > 2
	path[count(path) - 3] == "provisioner"
	path[count(path) - 2] == "local-exec"
	is_object(script)
}

deny contains msg if {
	count(steps) == 0
	msg := "no scripted step was found in any OpenTofu file, so this checked nothing. If they were all removed, remove this rule with them."
}

deny contains msg if {
	some [path, script] in steps
	contains(object.get(script, "command", ""), "pipefail")
	object.get(script, "interpreter", []) != ["/bin/bash", "-c"]
	msg := sprintf("%s: scripted steps set pipefail and do not name bash as what runs them. Left to the default the script runs under the machine's /bin/sh, and not every sh has pipefail: such a step runs from one machine and stops at its first line on another. Give the step interpreter = [\"/bin/bash\", \"-c\"].", [path])
}
