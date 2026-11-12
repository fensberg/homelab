# Every action a job runs is one the approved suppliers list names, and the
# list names nothing no job runs.
#
# The repository's allowed-actions setting is made from the list, so an
# action missing from it is refused by GitHub when the job starts, after the
# merge. This refuses it before: a new action is a reviewed change to the
# list, with a reason, before any workflow can use it. An entry nothing uses
# is refused too, so the list stays what the jobs run and no wider.
#
# An action is named whole, by its path in its repository when it is not at
# the root: GitHub allows each by that name, and the repository alone lets in
# only the one at its top.
#
# An action that only another action runs is in that action's code, not in
# a workflow here, so its entry says which listed actions run it. That is
# checked against what they run, at the commits they are pinned to, as
# .github/workflows/offline fetched it before the check (lib.pinned_actions,
# #881): every action a pinned action runs must be on the list, and an
# entry's called_by must be true of the commits pinned.
package rules.supplier_actions

import data.lib
import rego.v1

list := [f.contents | some f in lib.at("scripts/approved-suppliers.yml")][0]

entries := object.get(list, "actions", [])

# names is every action an entry lets in, by its whole name: the repository
# itself, or each path in it the entry names ("." is the root).
names(entry) := {name |
	some p in object.get(entry, "paths", ["."])
	name := full(entry.action, p)
}

full(action, ".") := action

full(action, p) := $"{action}/{p}" if p != "."

listed contains name if {
	some entry in entries
	some name in names(entry)
}

repositories contains entry.action if {
	some entry in entries
}

called_by(entry) := object.get(entry, "called_by", [])

# What each pinned action runs, as fetched: {ref: [what it runs]}.
fetched := lib.pinned_actions

# pinned is every action named here by owner and commit: the ones fetched.
pinned contains ref if {
	some [_, _, ref] in lib.uses
	regex.match(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(/[^@]+)?@[0-9a-f]{40}$`, ref)
}

name(ref) := split(ref, "@")[0]

repository(ref) := concat("/", array.slice(split(name(ref), "/"), 0, 2))

# run_by is every action the given repositories' pinned actions run.
run_by(callers) := {called | some [c, called] in runs; repository(c) in callers}

# runs is every [caller, what it runs] the fetch found, by whole name.
runs contains [name(caller), name(r)] if {
	some caller, called in fetched
	some r in called
}

deny contains msg if {
	count(listed) == 0
	msg := "the approved suppliers list names no action, so nothing says what a job may run"
}

deny contains msg if {
	some action in lib.outside_actions
	not action in listed
	msg := $"a workflow uses {action}, which is not in the approved suppliers list. Declare it under actions: with a reason, in a pull request of its own."
}

deny contains msg if {
	some entry in entries
	count(called_by(entry)) == 0
	some name in names(entry)
	not name in lib.outside_actions
	msg := $"the approved suppliers list names {name}, which no workflow uses. Take it off the list."
}

deny contains msg if {
	some entry in entries
	some caller in called_by(entry)
	not caller in repositories
	msg := $"the approved suppliers list says {caller} runs {entry.action}, but does not name {caller} itself"
}

deny contains msg if {
	some entry in entries
	count(called_by(entry)) > 0
	every caller in called_by(entry) {
		not caller in lib.outside_actions
	}
	msg := $"the approved suppliers list names {entry.action} for {concat(", ", called_by(entry))}, and no workflow uses any of them. Take it off the list."
}

deny contains msg if {
	count(fetched) == 0
	msg := "nothing says what the pinned actions run: PINNED_ACTIONS names no file .github/workflows/offline/pinned-actions.sh wrote, so no action an action runs was checked"
}

deny contains msg if {
	count(fetched) > 0
	some ref in pinned
	not fetched[ref]
	msg := $"{ref} is pinned here and was not fetched, so what it runs is unknown. Fetch them again with .github/workflows/offline/pinned-actions.sh."
}

deny contains msg if {
	some [caller, called] in runs
	not called in listed
	msg := $"{caller} runs {called}, which is not in the approved suppliers list. Declare it under actions:, with called_by naming the action that runs it."
}

deny contains msg if {
	count(fetched) > 0
	some entry in entries
	count(called_by(entry)) > 0
	some n in names(entry)
	not n in run_by(called_by(entry))
	msg := $"the approved suppliers list says {concat(" or ", called_by(entry))} runs {n}, and none does at the commit pinned"
}

deny contains msg if {
	count(fetched) > 0
	some entry in entries
	some caller in called_by(entry)
	count(run_by([caller]) & names(entry)) == 0
	msg := $"the approved suppliers list says {caller} runs {entry.action}, and it runs none of it at the commit pinned"
}
