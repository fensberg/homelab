# A job the suppliers list declares offline runs its check with no network.
#
# Such a job's check is run by .github/workflows/offline: what the lockfiles
# name is fetched first, then the check runs in the pull-request pools' image
# with `docker run --network none` (epoch 16, decision 16). A step of the
# job's own runs on the runner, which has a network, so an offline job has
# none: every step is an action, and one of them is offline. Its entry's
# hosts are what the fetch reaches, and nothing the check does.
package rules.offline_jobs

import data.lib
import rego.v1

list := [f.contents | some f in lib.at("scripts/approved-suppliers.yml")][0]

declared contains e.job if {
	some e in object.get(list, "egress", [])
	e.policy == "offline"
}

# key is how the suppliers list names a job: its workflow's file, and the job.
key(file, name) := $"{parts[count(parts) - 1]}/{name}" if parts := split(file, "/")

offline_steps(job) := [s | some s in object.get(job, "steps", []); s.uses == "$/.github/workflows/offline"]

deny contains msg if {
	count(declared) == 0
	msg := "no job in the egress section is offline, so nothing runs a pull request's check with no network"
}

deny contains msg if {
	some k in declared
	not k in {key(file, name) | some [file, name, _] in lib.jobs}
	msg := $"the suppliers list declares {k} offline, and no workflow has that job"
}

deny contains msg if {
	some [file, name, job] in lib.jobs
	key(file, name) in declared
	some i, step in object.get(job, "steps", [])
	step.run
	msg := $"{key(file, name)} is offline, and its step {i + 1} ({object.get(step, "name", "unnamed")}) runs on the runner, with a network. Run it through $/.github/workflows/offline."
}

deny contains msg if {
	some [file, name, job] in lib.jobs
	key(file, name) in declared
	count(offline_steps(job)) == 0
	msg := $"{key(file, name)} is offline, and no step of it runs $/.github/workflows/offline, so nothing runs its check with no network"
}
