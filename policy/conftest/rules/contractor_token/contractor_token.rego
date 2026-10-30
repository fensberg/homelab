# A step that runs the contractor hands it the token it fetches the site's
# release with, which it does before anything but sterilize.
package rules.contractor_token

import data.lib
import rego.v1

contractor_runs contains [file, where, step, args, envs] if {
	some [file, where, step, envs] in lib.steps_with_env
	some m in regex.find_all_string_submatch_n(`(?m)toolshed/contractor[ \t]+([a-z][^\n]*)`, object.get(step, "run", ""), -1)
	args := m[1]
}

hands_over(envs) if {
	some env in envs
	some name in ["GH_TOKEN", "GITHUB_TOKEN"]
	object.get(env, name, "") != ""
}

deny contains msg if {
	count(contractor_runs) == 0
	msg := "found no step that runs the contractor, so the contractor-token rule checked nothing: the reading has stopped matching"
}

deny contains msg if {
	some [file, where, step, args, envs] in contractor_runs
	not regex.match(`-phase[ =]sterilize\b`, args)
	not hands_over(envs)
	msg := $"{file}{where}, step {trim_space(object.get(step, "name", ""))} runs the contractor and hands it no GitHub token. The contractor fetches the release the site runs before anything it does but sterilize, and is refused at Render without a token. Give the step GH_TOKEN, the job's own token."
}
