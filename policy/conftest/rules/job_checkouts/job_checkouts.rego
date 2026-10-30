# Every checkout refuses to persist credentials, in the job and inside every
# composite action it reaches: a token left on disk is reachable by every
# later step of the job, wherever the checkout was written.
#
# Rego has no recursion, so the actions a job reaches are followed one level
# at a time, four deep; one that reaches further, or loops back on itself, is
# refused rather than passed unread.
package rules.job_checkouts

import data.lib
import rego.v1

level0 contains [file, name, "", step] if {
	some [file, name, job] in lib.jobs
	some step in object.get(job, "steps", [])
}

level1 contains entry if {
	some parent in level0
	some entry in inside(parent)
}

level2 contains entry if {
	some parent in level1
	some entry in inside(parent)
}

level3 contains entry if {
	some parent in level2
	some entry in inside(parent)
}

level4 contains entry if {
	some parent in level3
	some entry in inside(parent)
}

inside(parent) := {[parent[0], parent[1], $"{parent[2]} (inside {parent[3].uses})", inner] |
	dir := lib.same_repo_action(object.get(parent[3], "uses", ""))
	some inner in lib.steps_of(dir)
}

reached := ((level0 | level1) | level2) | level3

checkouts contains entry if {
	some entry in reached
	contains(object.get(entry[3], "uses", ""), "actions/checkout@")
}

deny contains msg if {
	count(checkouts) == 0
	msg := "no checkout was found in any job, at any depth, so the checkout law checked nothing"
}

deny contains msg if {
	some [file, name, where, step] in checkouts
	object.get(object.get(step, "with", {}), "persist-credentials", null) != false
	msg := $"{file}: job \"{name}\" checks out without persist-credentials: false{where}. The job's token stays on disk for every later step otherwise, and anything one of those steps runs can push with it."
}

deny contains msg if {
	some [file, name, where, step] in level4
	msg := $"{file}: job \"{name}\" reaches this repository's actions more than four deep{where}, or round in a loop, so what it checks out cannot be read"
}

deny contains msg if {
	some [file, name, where, step] in reached
	uses := object.get(step, "uses", "")
	dir := lib.same_repo_action(uses)
	not lib.actions[dir]
	msg := $"{file}: job \"{name}\" uses {uses}{where}, which cannot be read"
}
