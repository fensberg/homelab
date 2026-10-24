# Shared readings of the configuration, for the rules beside this file.
#
# Conftest hands every rule one input: every file it parsed, as
# {path, contents}, with each document of a multi-document YAML file an entry
# of its own under the same path. What a rule needs is found here by what a
# file is, not by a list of paths.
#
# THE ONLY READER OF THE INPUT. A rule reads its files through the selectors
# here and touches the input nowhere else, so every place a rule reads is a
# selector a guard can name and prove (TestOnlyTheSelectorsReadTheInput,
# agreed 2026-10-18).
package lib

import rego.v1

# objects is every Kubernetes object: each document that is a mapping with a
# kind, wherever it was read from.
objects contains f if {
	some f in input
	is_object(f.contents)
	is_string(f.contents.kind)
}

# opentofu is every OpenTofu file.
opentofu contains f if {
	some f in input
	endswith(f.path, ".tf")
}

# plan is every resource a plan of a site's roots would make, as
# [path, resource]: the plans the rule harness makes from nothing, with a
# stand-in for every value the vault supplies, each resource with its
# address, its type and the values it would be made with, wherever in the
# plan's modules it is declared. A plan is found by what it is - what
# `tofu show -json` writes of a saved plan - and not by where it was written.
plan contains [f.path, r] if {
	some f in input
	is_object(f.contents)
	is_object(f.contents.planned_values)
	walk(f.contents.planned_values, [_, r])
	is_object(r)
	is_string(r.address)
	is_string(r.type)
	is_object(r.values)
}

# plan_configuration is every resource as a plan's configuration declares
# it, as [path, address, resource]: its address with the modules it is
# declared in, and each of its arguments as the plan gives an expression -
# a constant_value when it is written as one, and the references it is
# worked out from when it is not. What a value is made from, where the
# values alone cannot say: a value worked out from a stand-in for the vault
# reads as the stand-in.
plan_configuration contains [f.path, address, r] if {
	some f in input
	is_object(f.contents)
	is_object(f.contents.configuration)
	walk(f.contents.configuration, [p, r])
	is_object(r)
	is_string(r.address)
	is_string(r.mode)
	is_object(r.expressions)
	calls := [sprintf("module.%s", [p[i + 1]]) | some i, k in p; k == "module_calls"]
	address := concat(".", array.concat(calls, [r.address]))
}

# provider_locks is every provider a lock file locks, as [path, source]:
# what `tofu init` writes beside a root or a module to say which providers
# it was initialised with and the hashes they must match. A lock file is
# found by what it is - providers, each with its hashes - and not by its
# name.
provider_locks contains [f.path, source] if {
	some f in input
	is_object(f.contents)
	is_object(f.contents.provider)
	some source, locked in f.contents.provider
	some each in locked
	is_array(each.hashes)
}

# dockerfiles is every Dockerfile, as Conftest reads one: its instructions in
# order, each with the stage it is in.
dockerfiles contains f if {
	some f in input
	is_array(f.contents)
	count(f.contents) > 0
	every i in f.contents {
		is_object(i)
		i.Cmd
	}
}

# at is every document of the file at a path, from the repository's top.
at(path) := [f | some f in input; f.path == path]

# under is every document of every file beneath a directory, given with its
# trailing slash.
under(prefix) := [f | some f in input; startswith(f.path, prefix)]

# named is every document of every file of one name, in whatever directory.
named(name) := [f | some f in input; endswith(f.path, sprintf("/%s", [name]))]

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

# runnable is every file whose uses GitHub executes, by its path: a workflow,
# or the metadata of a composite action anywhere.
runnable[path] := doc if {
	some path, doc in workflows
}

runnable[f.path] := f.contents if {
	some f in input
	parts := split(f.path, "/")
	parts[count(parts) - 1] in {"action.yml", "action.yaml"}
}

# uses is every action a runnable file names, wherever in it: [file, where, ref].
uses contains [file, where, ref] if {
	some file, doc in runnable
	walk(doc, [where, ref])
	count(where) > 0
	where[count(where) - 1] == "uses"
	is_string(ref)
}

# triggers is the events a workflow runs on. YAML 1.1 reads the bare key `on`
# as the boolean true, which is how Conftest's reader hands it over, so both
# spellings are read; a list, a mapping or one name are all forms GitHub takes.
triggers(doc) := names if {
	on := object.get(doc, "on", object.get(doc, "true", null))
	names := trigger_names(on)
}

trigger_names(on) := {on} if is_string(on)

trigger_names(on) := {e | some e in on} if is_array(on)

trigger_names(on) := {e | some e, _ in on} if is_object(on)

trigger_names(null) := set()

# steps_with_env is every step of a runnable file with the environment it
# runs in: [file, where, step, environments], the step's own last.
steps_with_env contains [file, sprintf(", job %s", [name]), step, [object.get(doc, "env", {}), object.get(job, "env", {}), object.get(step, "env", {})]] if {
	some file, doc in runnable
	some name, job in object.get(doc, "jobs", {})
	some step in object.get(job, "steps", [])
}

steps_with_env contains [file, "", step, [object.get(doc, "env", {}), object.get(step, "env", {})]] if {
	some file, doc in runnable
	some step in object.get(object.get(doc, "runs", {}), "steps", [])
}
