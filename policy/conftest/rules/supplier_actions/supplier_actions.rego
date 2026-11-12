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
# An action that only another action runs is invisible here - it is in that
# action's code, not in a workflow - so its entry says which listed actions
# run it, and stands while one of them is used.
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
