# No workflow reaches one of this repository's own actions by a relative path.
#
# `uses: ./.github/...` resolves against whatever the workspace holds when the
# step runs: it needs a checkout to have happened, and it reads the checkout's
# copy of the action and not the one at the commit being run. `$/` is GitHub's
# reference to the repository itself and has neither property, which is why
# zizmor's self-repository audit asks for it. zizmor owns the finding; this
# says the same thing in seconds on a machine without zizmor, and names the
# file.
package rules.local_action_refs

import data.lib
import rego.v1

deny contains msg if {
	count(lib.workflows) == 0
	msg := "found no workflows, so none was read for how it reaches a local action"
}

deny contains msg if {
	some [file, _, ref] in lib.uses
	startswith(ref, "./")
	msg := $"{file} reaches a local action with a relative `uses: {ref}`. Write it as `uses: $/{trim_prefix(ref, "./")}`: it resolves at the commit being run, with no checkout, and cannot be redirected by whatever a workspace holds."
}
