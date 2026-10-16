# What every workflow says about how its steps run, and what they read.
package workflow_shape

import data.lib
import rego.v1

deny contains msg if {
	count(lib.workflows) == 0
	msg := "found no workflows, so no workflow's shape was checked"
}

# Every workflow defaults to a pipefail shell. Naming bash gets
# `bash --noprofile --norc -eo pipefail {0}`; GitHub's default is `bash -e
# {0}`, where a failing command in a pipeline is hidden by what it pipes into.
deny contains msg if {
	some file, doc in lib.workflows
	object.get(object.get(object.get(doc, "defaults", {}), "run", {}), "shell", "") != "bash"
	msg := sprintf("%s does not set a pipefail shell. Add defaults: {run: {shell: bash}} above its jobs; without it every run: step runs under `bash -e {0}`, where a failing command in a pipeline is hidden by whatever it pipes into.", [file])
}

# No step reads $TMPDIR. GitHub-hosted runners do not set it, so under set -u
# a step fails before doing anything (#440). $RUNNER_TEMP is set everywhere.
deny contains msg if {
	some file, doc in lib.workflows
	walk(doc, [where, v])
	is_string(v)
	some line in split(v, "\n")
	not startswith(trim_space(line), "#")
	regex.match(`\$\{?TMPDIR\b`, line)
	msg := sprintf("%s reads $TMPDIR at %s. GitHub-hosted runners do not set TMPDIR, so under `set -u` this is \"TMPDIR: unbound variable\" and the step fails before doing anything (#440). Use $RUNNER_TEMP, which Actions sets on every runner.", [file, concat(".", [sprintf("%v", [p]) | some p in where])])
}

# An App token's inputs are fed the value their names say: client-id the
# App's Client ID, app-id its numeric id. One fed the other failed every
# mint for a week with "Integration not found" (#439).
token_inputs contains [file, field, secret] if {
	some file, doc in lib.workflows
	walk(doc, [where, v])
	count(where) > 0
	field := where[count(where) - 1]
	field in {"client-id", "app-id"}
	is_string(v)
	m := regex.find_all_string_submatch_n(`^\s*\$\{\{\s*(?:secrets|vars)\.([A-Za-z0-9_]+)\s*\}\}`, v, 1)[0]
	secret := m[1]
}

wanted := {"client-id": "_CLIENT_ID", "app-id": "_APP_ID"}

deny contains msg if {
	some [file, field, secret] in token_inputs
	not endswith(secret, wanted[field])
	msg := sprintf("%s passes %s to `%s`. That input takes a value the name should say: client-id is the App's Client ID (beginning \"Iv\"), and app-id is the numeric App ID. A secret named for one and fed to the other is how the expedite duty failed every token mint for a week with \"Integration not found\" (#439). Name it *%s, and store the value that name says.", [file, secret, field, wanted[field]])
}

deny contains msg if {
	count(token_inputs) == 0
	msg := "no workflow passes a secret to a GitHub App token input, so the token-input rule checked nothing"
}

# A step that runs the contractor hands it the token it fetches the site's
# release with, which it does before anything but sterilize.
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
	msg := sprintf("%s%s, step %s runs the contractor and hands it no GitHub token. The contractor fetches the release the site runs before anything it does but sterilize, and is refused at Render without a token. Give the step GH_TOKEN, the job's own token.", [file, where, trim_space(object.get(step, "name", ""))])
}

# A concurrency group names the subject whose newer run replaces it: a pull
# request's number, a comment's issue and the comment itself, and the event
# when a pull request shares a workflow with another (#573, #574).
groups contains [file, file, doc.concurrency, lib.triggers(doc)] if {
	some file, doc in lib.workflows
	doc.concurrency
}

groups contains [file, sprintf("%s job %s", [file, name]), job.concurrency, lib.triggers(doc)] if {
	some file, doc in lib.workflows
	some name, job in object.get(doc, "jobs", {})
	job.concurrency
}

group_of(c) := c if is_string(c)

group_of(c) := c.group if is_object(c)

needs(triggers) := {[ref, why] |
	some [ref, why, when] in [
		["github.event.pull_request.number", "so it is not keyed by the pull request: a merged pull request reports its base branch as github.ref, and its closing run cancels whatever runs there (#573)", pr(triggers)],
		["github.event.issue.number", "so every comment shares one group: a comment event has no pull_request (#574)", "issue_comment" in triggers],
		["github.event.comment", "so a comment that asks for nothing still replaces a run in progress (#574)", "issue_comment" in triggers],
		["github.event_name", "so a pull request's run can share a group with another event's and cancel it (#573)", pr_and_other(triggers)],
	]
	when
}

prs := {"pull_request", "pull_request_target"}

pr(triggers) := count(triggers & prs) > 0

pr_and_other(triggers) := true if {
	pr(triggers)
	count(triggers - prs) > 0
}

pr_and_other(triggers) := false if not pr(triggers)

pr_and_other(triggers) := false if count(triggers - prs) == 0

deny contains msg if {
	count(groups) == 0
	msg := "no concurrency group was found in any workflow, so the concurrency rule examined nothing"
}

deny contains msg if {
	some [_, where, c, triggers] in groups
	group := group_of(c)
	some [ref, why] in needs(triggers)
	not contains(group, ref)
	msg := sprintf("%s: the concurrency group %q does not use %s, %s", [where, group, ref, why])
}
