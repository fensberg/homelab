# The job that runs a branch's code cannot write, and the job that writes runs
# none. The plan builds and runs the branch's contractor, so it only reads;
# the comment is another job's, which runs only the scripts beside the
# github-script action and takes the plan as an artifact.
package rules.plan_writer

import data.lib
import rego.v1

plan_workflow := ".github/workflows/plan-infrastructure.yml"

writable(job) := sort([scope | some scope, level in object.get(job, "permissions", {}); level == "write"])

runs_the_contractor(job) if {
	some step in object.get(job, "steps", [])
	contains(object.get(step, "run", ""), "scripts/contractor")
}

plan_jobs contains [name, job] if {
	some name, job in lib.workflows[plan_workflow].jobs
	runs_the_contractor(job)
}

writer_jobs contains [name, job] if {
	some name, job in lib.workflows[plan_workflow].jobs
	count(writable(job)) > 0
}

deny contains msg if {
	some [name, job] in plan_jobs
	count(object.get(job, "permissions", {})) == 0
	msg := sprintf("the job %s builds and runs the branch's contractor and states no permissions of its own, so its token is whatever the workflow or the repository gives", [name])
}

deny contains msg if {
	some [name, job] in plan_jobs
	count(writable(job)) > 0
	msg := sprintf("the job %s builds and runs the branch's contractor with a token that can write %s. What plans only reads; the comment is another job's.", [name, concat(", ", writable(job))])
}

deny contains msg if {
	some [name, job] in writer_jobs
	some step in job.steps
	object.get(step, "run", "") != ""
	msg := sprintf("the job %s can write %s and runs a command of its own (%q). The job that writes runs only the scripts beside the github-script action.", [name, concat(", ", writable(job)), object.get(step, "name", "")])
}

deny contains msg if {
	some [name, job] in writer_jobs
	some step in job.steps
	endswith(object.get(step, "uses", ""), "/mobilize")
	object.get(object.get(step, "with", {}), "checkout", "") != "false"
	msg := sprintf("the job %s can write %s and checks the branch out. It needs nothing from the repository: the plan arrives as an artifact.", [name, concat(", ", writable(job))])
}

deny contains msg if {
	lib.workflows[plan_workflow]
	count(plan_jobs) == 0
	msg := "no job of the plan workflow runs the contractor, so this has stopped reading the job it guards"
}

deny contains msg if {
	lib.workflows[plan_workflow]
	count(writer_jobs) == 0
	msg := "no job of the plan workflow can write to the pull request, so the plan is never reported"
}

deny contains msg if {
	not lib.workflows[plan_workflow]
	msg := sprintf("%s is not there, so the job that runs a branch's code is not read", [plan_workflow])
}
