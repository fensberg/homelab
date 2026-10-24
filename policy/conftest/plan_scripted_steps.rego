# A step OpenTofu runs as a script names bash as what runs it.
#
# Left to the default, a local-exec step runs under the machine's /bin/sh,
# and which sh that is depends on the machine. The step that applies the
# network manifest is bash: its first line sets `pipefail`. It named no
# interpreter, so it worked from a workstation whose sh takes that option,
# and on the estate's own runner it stopped within milliseconds of starting,
# before it had asked the cluster anything. A change to the network could
# then be delivered by hand and never by a merge, and nothing said so until
# a merge tried (2026-10-11).
#
# ON THE PLAN'S CONFIGURATION, which gives each step's interpreter and not
# its script's text: so every step names bash, not only the ones that read
# as bash. A script is shell embedded in OpenTofu, which becomes structure
# rather than text (the refactor's shell step).
package rules.plan_scripted_steps

import data.lib
import rego.v1

bash := ["/bin/bash", "-c"]

steps contains [address, step] if {
	some [_, address, r] in lib.plan_configuration
	some step in object.get(r, "provisioners", [])
	step.type == "local-exec"
}

deny contains msg if {
	count(steps) == 0
	msg := "no scripted step is in any plan's configuration, so this checked nothing. If they were all removed, remove this rule with them."
}

deny contains msg if {
	some [address, step] in steps
	object.get(object.get(step.expressions, "interpreter", {}), "constant_value", []) != bash
	msg := sprintf("%s: scripted steps do not name bash as what runs them. Left to the default the script runs under the machine's /bin/sh, and not every sh has pipefail: such a step runs from one machine and stops at its first line on another. Give the step interpreter = [\"/bin/bash\", \"-c\"].", [address])
}
