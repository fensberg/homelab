# What scripts/versions.env pins, it alone pins: no workflow writes down a
# version of its own, whether as the pin's own key or as a setup action's
# version input.
#
# versions.env is only "the one place a version is pinned" if nothing else
# writes one down. Workflows were the leak: pr-validation.yml carried its own
# TOFU_VERSION for long enough that CI validated pull requests on OpenTofu
# 1.10.6 while the estate was converged with 1.12.6 - two minor versions
# apart, agreeing with nothing, reported by nobody.
#
# This only rejects an *assignment* of a literal. Referring to a pinned
# version is the entire point (`--build-arg TOFU_VERSION="$TOFU_VERSION"`,
# `${{ env.GO_VERSION }}`), so a value that starts with $ is a reference and
# passes.
#
# That every pin is one a workflow can read is rules.version_exports.
package rules.version_pins

import data.lib
import rego.v1

# A brace of a template a message quotes, which a template string cannot
# hold as it is.
open := "{"

close := "}"

pins := [f.contents | some f in lib.at("scripts/versions.env")][0]

literal := `^["']?[^$\s"']`

# A pinned key given a literal value, as YAML or as a line of a script.
declares contains [file, key] if {
	some file, doc in lib.workflows
	walk(doc, [path, v])
	count(path) > 0
	key := path[count(path) - 1]
	pins[key]
	not is_object(v)
	not is_array(v)
	regex.match(literal, $"{v}")
}

declares contains [file, key] if {
	some file, doc in lib.workflows
	walk(doc, [_, v])
	is_string(v)
	some key, _ in pins
	regex.match($`(?m)^\s*{key}\s*[:=]\s*["']?[^$\s"']`, v)
}

# Nor does a setup action restate one as its version input.
#
# WHAT THIS GUARDS. versions.env claimed, from the day it was written, that a
# test asserted no consumer restates a key - and none did (#165). Four
# workflows carried their own `go-version: "1.26.7"`, including
# deploy-infrastructure, the lane that reaches real hardware. They agreed with
# the pin that day. Nothing kept them agreeing, and the same drift had
# already happened once here, as the TOFU_VERSION above.
#
# The rule is shape, not value: every `<tool>-version:` input to a setup action
# reads the pin through the environment the versions action exports. Checking
# values instead would miss a restatement that happens to agree today, which is
# the exact state all four were in.
setup_input := `^(?:go|node|python|java|dotnet|tofu|opentofu|terraform|helm|kubectl)[-_]version$`

inputs contains [file, key, v] if {
	some file, doc in lib.runnable
	walk(doc, [path, v])
	count(path) > 0
	key := path[count(path) - 1]
	is_string(key)
	regex.match(setup_input, key)
}

deny contains msg if {
	count(pins) == 0
	msg := "versions.env declares nothing, so this rule proves nothing"
}

deny contains msg if {
	some [file, key] in declares
	msg := $"{file} declares {key} itself. versions.env owns that pin. Read it with `uses: ./.github/actions/versions` and refer to ${open}{open} env.{key} {close}{close} instead - a second declaration is a version that drifts silently."
}

deny contains msg if {
	some [file, key, v] in inputs
	not startswith($"{v}", "${{")
	msg := $"{file} restates a pinned version: {key}: {v}. Read it from scripts/versions.env instead: add the step `uses: ./.github/actions/versions` after the checkout, and write {key}: ${open}{open} env.<KEY> {close}{close}. A literal agrees with the pin only until the pin moves, and nothing says when it stops agreeing."
}

deny contains msg if {
	count(inputs) < 2
	msg := $"found only {count(inputs)} version input(s) - the pattern has stopped matching, so this rule proves nothing"
}
