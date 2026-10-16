# Shared readings of the configuration, for the rules beside this file.
#
# Conftest hands every rule one input: every file it parsed, as
# {path, contents}, with each document of a multi-document YAML file an entry
# of its own under the same path. What a rule needs is found here by what a
# file is, not by a list of paths.
package lib

import rego.v1

# workflows is every workflow, by its path: a YAML file directly under
# .github/workflows.
workflows[f.path] := f.contents if {
	some f in input
	startswith(f.path, ".github/workflows/")
	count(split(f.path, "/")) == 3
	is_yml(f.path)
}

# actions is every composite action this repository defines, by the
# directory a step names it with.
actions[dir] := f.contents if {
	some f in input
	parts := split(f.path, "/")
	parts[count(parts) - 1] in {"action.yml", "action.yaml"}
	dir := concat("/", array.slice(parts, 0, count(parts) - 1))
}

is_yml(path) if endswith(path, ".yml")

is_yml(path) if endswith(path, ".yaml")

# same_repo_action is the directory a step's uses names, when it names one of
# this repository's own actions: `$/` is GitHub's reference to the repository
# itself, and `./` a path in the checkout.
same_repo_action(uses) := dir if {
	some prefix in ["$/", "./"]
	startswith(uses, prefix)
	dir := trim_suffix(trim_prefix(uses, prefix), "/")
}

# jobs is every job of every workflow: [file, name, job].
jobs contains [file, name, job] if {
	some file, doc in workflows
	some name, job in doc.jobs
}

# steps_of is a composite action's steps, or nothing when it is not one.
steps_of(dir) := steps if {
	actions[dir].runs.using == "composite"
	steps := actions[dir].runs.steps
}
