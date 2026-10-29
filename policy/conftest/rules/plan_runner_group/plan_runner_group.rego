# Every workflow that asks for one of the estate's own runners is one their
# group admits, and the group admits no other.
#
# WHY. The estate sets, at its source control, which workflows may use its
# runners, and the platform gives a runner to no other. A workflow moved onto
# the estate's runners without being named there is found out by the platform
# after the merge, as a job that waits for a runner for ever; and a name left
# there after its workflow stopped using them is a workflow that may reach the
# estate's network with nobody having decided it should.
#
# IT ALSO HOLDS THE GROUP TO BEING A WALL: one that admits chosen repositories
# and chosen workflows.
#
# ONE GROUP MAY TAKE ANY WORKFLOW, and only one kind: the group the pools of
# pull-request runners join. A pull request runs its own copy of a workflow,
# from its own ref, so no list of workflows at a branch can admit it. That
# group is held another way - its runners are on a machine closed to the rest
# of the estate - and it is allowed only while every set of runners that joins
# it is a pull-request pool, and none of the estate's own does.
#
# ON WHAT TOFU WOULD MAKE. The groups are read off the estate's plan, with
# every value worked out; the workflows off the workflows; and which group
# each set of runners joins off the manifests Flux applies.
package rules.plan_runner_group

import data.lib
import rego.v1

groups contains r if {
	some [path, r] in lib.plan
	lib.plan_scope(path) == "estate"
	r.mode == "managed"
	r.type == "github_actions_runner_group"
}

# A group that takes any workflow is named, for what joins it to be judged.
open_groups contains r.values.name if {
	some r in groups
	r.values.restricted_to_workflows == false
}

walled contains r if {
	some r in groups
	r.values.restricted_to_workflows == true
}

# The workflow file a group admits, from owner/repository/.github/workflows/
# <file>@<ref>.
file_of(w) := file if {
	path := split(w, "@")[0]
	parts := split(path, "/")
	file := parts[count(parts) - 1]
}

admitted contains file_of(w) if {
	some r in walled
	some w in object.get(r.values, "selected_workflows", [])
	is_string(w)
}

# A label is the platform's, a pull-request pool's, or the estate's own:
# anything else - a group, an expression - is the estate's own, since nothing
# here can say it is not.
estates_own(runs_on) if {
	runs_on != null
	count(lib.runner_labels(runs_on)) == 0
}

estates_own(runs_on) if {
	some label in lib.runner_labels(runs_on)
	not lib.hosted_runner(label)
	not label in lib.pull_request_pools
}

base(path) := parts[count(parts) - 1] if parts := split(path, "/")

asking contains base(file) if {
	some [file, _, job] in lib.jobs
	estates_own(object.get(job, "runs-on", null))
}

uses_a_pool if {
	some [_, _, job] in lib.jobs
	some label in lib.runner_labels(object.get(job, "runs-on", null))
	label in lib.pull_request_pools
}

# Each set of runners, by the group it joins and whether it is a pool: a pool
# is a set whose name begins as a pool's label does.
sets contains [group, is_pool] if {
	some f in lib.objects
	f.contents.kind == "HelmRelease"
	f.contents.spec.chart.spec.chart == "gha-runner-scale-set"
	group := f.contents.spec.values.runnerGroup
	set := f.contents.spec.values.runnerScaleSetName
	is_pool := count({p | some p in lib.pull_request_pools; startswith(set, split(p, "${{")[0])}) > 0
}

deny contains msg if {
	some r in groups
	r.values.visibility != "selected"
	msg := $"{r.address} admits repositories nobody chose: visibility is not \"selected\""
}

deny contains msg if {
	some r in groups
	not is_boolean(object.get(r.values, "restricted_to_workflows", null))
	msg := $"{r.address} does not say whether it admits any workflow: restricted_to_workflows is unset"
}

deny contains msg if {
	some r in walled
	count([w | some w in object.get(r.values, "selected_workflows", []); is_string(w)]) == 0
	msg := $"{r.address} names no workflow it admits: selected_workflows is empty"
}

deny contains msg if {
	some name in open_groups
	count({s | some s in sets; s[0] == name}) == 0
	msg := $"the group {name} takes any workflow and no set of runners joins it: a group for nothing"
}

deny contains msg if {
	some name in open_groups
	[name, false] in sets
	msg := $"the group {name} takes any workflow, and a set of the estate's own runners joins it. Only the pull-request pools, on a machine of their own, may be in a group no list of workflows holds"
}

deny contains msg if {
	some [group, true] in sets
	not group in open_groups
	msg := $"a pull-request pool joins the group {group}, which takes only named workflows from a branch. A pull request runs from neither, so its jobs would wait for a runner for ever"
}

deny contains msg if {
	uses_a_pool
	count(open_groups) == 0
	msg := "a workflow asks for a pull-request pool and no group is declared for the pools to join"
}

deny contains msg if {
	some w in asking
	not w in admitted
	msg := $"{w} asks for the estate's runner and no group admits it: it would wait for a runner for ever. Name it where the group's workflows are given"
}

deny contains msg if {
	some w in admitted
	not w in asking
	msg := $"{w} is admitted to the estate's runners and no job of it asks for one. Take it out of the group's workflows"
}

deny contains msg if {
	count(groups) == 0
	msg := "no group of runners is in the estate's plan, so nothing says which work the estate's runners take"
}

deny contains msg if {
	count(asking) == 0
	msg := "no workflow asks for the estate's runner, and a converge runs on one: the reading is wrong"
}

deny contains msg if {
	count(lib.workflows) < 5
	msg := $"only {count(lib.workflows)} workflow(s) were read, so this checked almost nothing"
}
