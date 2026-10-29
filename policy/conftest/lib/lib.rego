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

# plan is every resource a plan of the estate's or a site's roots would
# make, as [path, resource] - plan_scope says which: the plans the rule
# harness makes from nothing, with a stand-in for every value the vault
# supplies, each resource with its address, its type and the values it would
# be made with, wherever in the plan's modules it is declared. A plan is found by what it is - what
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
# declared in, and each of its arguments as the plan gives an expression
# (expressions, which a resource written with no arguments has none of) -
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
	is_string(r.type)
	calls := [$"module.{p[i + 1]}" | some i, k in p; k == "module_calls"]
	address := concat(".", array.concat(calls, [r.address]))
}

# plan_module_calls is every module a plan's root calls, as [path, name,
# call]: the call as the plan's configuration gives it, its source as it was
# resolved for that plan.
plan_module_calls contains [f.path, name, call] if {
	some f in input
	is_object(f.contents)
	is_object(f.contents.configuration)
	some name, call in object.get(f.contents.configuration.root_module, "module_calls", {})
}

# plan_outputs is every output a plan's root declares, as [path, name,
# output]: the output as the plan's configuration gives it, its expression's
# references among it.
plan_outputs contains [f.path, name, output] if {
	some f in input
	is_object(f.contents)
	is_object(f.contents.configuration)
	some name, output in object.get(f.contents.configuration.root_module, "outputs", {})
}

# plan_variables is every variable a plan's root declares, as [path, name,
# declared, given]: the declaration as the configuration has it, and the
# value the plan was made with, or null when it was given none.
plan_variables contains [f.path, name, declared, given] if {
	some f in input
	is_object(f.contents)
	is_object(f.contents.configuration)
	some name, declared in object.get(f.contents.configuration.root_module, "variables", {})
	given := object.get(object.get(object.get(f.contents, "variables", {}), name, {}), "value", null)
}

# plan_scope is the scope of the root a plan is of: "site" for a site's
# roots, "estate" for every other, as the rule harness writes each plan under
# the directory of its scope. A node has no root of its own, so those are the
# two. A plan written anywhere else has no scope, and a rule asking for one
# is told nothing.
plan_scope(path) := scope if {
	parts := split(path, "/")
	count(parts) >= 2
	scope := parts[count(parts) - 2]
	scope in {"site", "estate"}
}

# pull_request_pools is the label a job asks for each of the pools of
# pull-request runners by, made from the names the suppliers list declares
# under pull_request_pools - the one place a pool is named. The site is the
# repository's variable, so a label ends in that and never in a site written
# out.
pull_request_pools contains $"pr-{name}-$\{\{ vars.pull_request_runners_site }}" if {
	some f in at("scripts/approved-suppliers.yml")
	some name in f.contents.pull_request_pools.names
}

# pull_request_pool is whether a set of runners is one of the pull-request
# pools: its name begins as a pool's label does, before the site.
pull_request_pool(set_name) if {
	some label in pull_request_pools
	startswith(set_name, split(label, "${{")[0])
}

# hosted_runner is whether a label is a runner GitHub provides.
hosted_runner(label) if regex.match(`^(ubuntu|windows|macos)-[a-z0-9.-]+$`, label)

# runner_labels is the runners a job's runs-on asks for, read as written: one
# name, or each name of a list. Anything else - a group, an expression -
# names no runner, and is read as none; a name in a list that is not text
# reads as empty.
runner_labels(runs_on) := [runs_on] if is_string(runs_on)

runner_labels(runs_on) := [label_of(l) | some l in runs_on] if is_array(runs_on)

runner_labels(runs_on) := [] if {
	not is_string(runs_on)
	not is_array(runs_on)
}

label_of(l) := l if is_string(l)

label_of(l) := "" if not is_string(l)

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
named(name) := [f | some f in input; endswith(f.path, $"/{name}")]

# facts is every fact, by its name: a JSON file directly under config/facts,
# each a value more than one language here needs and written there once.
facts[name] := f.contents if {
	some f in input
	parts := split(f.path, "/")
	count(parts) == 3
	parts[0] == "config"
	parts[1] == "facts"
	endswith(parts[2], ".json")
	name := trim_suffix(parts[2], ".json")
}

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

trigger_names(on) := object.keys(on) if is_object(on)

trigger_names(null) := set()

# steps_with_env is every step of a runnable file with the environment it
# runs in: [file, where, step, environments], the step's own last.
steps_with_env contains [file, $", job {name}", step, [object.get(doc, "env", {}), object.get(job, "env", {}), object.get(step, "env", {})]] if {
	some file, doc in runnable
	some name, job in object.get(doc, "jobs", {})
	some step in object.get(job, "steps", [])
}

steps_with_env contains [file, "", step, [object.get(doc, "env", {}), object.get(step, "env", {})]] if {
	some file, doc in runnable
	some step in object.get(object.get(doc, "runs", {}), "steps", [])
}
