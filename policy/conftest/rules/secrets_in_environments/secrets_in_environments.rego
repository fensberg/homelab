# A secret reaches a job only through an environment (#889).
#
# A repository secret is handed to any workflow run from any branch here,
# including one changed on a pull request's own branch: nothing on the
# platform stands between a branch and the key. An environment does. The
# estate holds each of its environments to the default branch, and the
# platform refuses an environment's secrets to a job from anywhere else,
# before the job is handed anything. So a job that reads a secret names an
# environment, and a secret lives there and not in the repository.
#
# Deny by default: every read of the secrets context counts, by any
# spelling, except GITHUB_TOKEN - the run's own token, minted for it and gone
# with it. A read this cannot name is refused as one, not passed over.
package rules.secrets_in_environments

import data.lib
import rego.v1

# The run's own token, which no environment holds.
own_token := "GITHUB_TOKEN"

# expressions is every expression in a value: each ${{ }}, and every if,
# which is an expression without them.
expressions(node) := {m[1] |
	walk(node, [_, v])
	is_string(v)
	some m in regex.find_all_string_submatch_n(`\$\{\{([\s\S]*?)\}\}`, v, -1)
} | {v |
	walk(node, [path, v])
	is_string(v)
	count(path) > 0
	path[count(path) - 1] == "if"
}

# read is what a read of the secrets context names, when it names one.
read(name) := name if name != ""

else := "a secret, read by a name this cannot read"

# secrets_read is every secret a value reads, by name.
secrets_read(node) := {name |
	some e in expressions(node)
	some m in regex.find_all_string_submatch_n(`\bsecrets\b(?:\s*\.\s*([A-Za-z_][A-Za-z0-9_]*))?`, e, -1)
	name := read(m[1])
	name != own_token
}

deny contains msg if {
	some [file, name, job] in lib.jobs
	not job.environment
	some secret in secrets_read(job)
	msg := $"job {name} in {file} reads {secret} and names no environment, so it is handed whatever the repository holds, from any branch. Keep the secret in an environment the estate holds to the default branch, and name it here."
}

deny contains msg if {
	some file, doc in lib.workflows
	some secret in secrets_read(object.remove(doc, ["jobs"]))
	msg := $"{file} reads {secret} outside any job, where every job is handed it and none names the environment it should come from"
}

deny contains msg if {
	some [file, name, job] in lib.jobs
	job.secrets == "inherit"
	msg := $"job {name} in {file} hands the workflow it calls every secret it can reach. Hand it the ones it needs, by name, from a job that names their environment."
}

deny contains msg if {
	count(lib.workflows) == 0
	msg := "no workflow was read, so no job was checked for the secrets it reads"
}
