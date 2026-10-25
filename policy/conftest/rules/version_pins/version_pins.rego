# What scripts/versions.env pins, it alone pins, and every pin is one the
# workflows can read.
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
# The composite action only exports a line whose key ends in _VERSION and
# whose value starts alphanumerically and holds nothing but
# [A-Za-z0-9._+-]. That pattern is a security boundary, not a convenience:
# this repository is public and pr-validation.yml runs on pull_request, so on
# a fork's branch versions.env is attacker-controlled, and a free-form
# KEY=VALUE written to $GITHUB_ENV is how LD_PRELOAD or NODE_OPTIONS becomes
# code execution in a later step.
#
# The cost of a narrow pattern is that a key outside it is skipped in
# silence, and the job then runs with an empty version rather than failing.
# So every key here must be one the action can actually export.
package rules.version_pins

import data.lib
import rego.v1

pins := [f.contents | some f in lib.at("scripts/versions.env")][0]

exporter := [f | some f in lib.at(".github/actions/versions/action.yml")]

deny contains msg if {
	count(pins) == 0
	msg := "versions.env declares nothing, so this rule proves nothing"
}

deny contains msg if {
	count(exporter) == 0
	msg := ".github/actions/versions/action.yml is missing. Without it a workflow cannot read scripts/versions.env, and the only way to give a job a version is to declare one - which is what this rule forbids."
}

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
	regex.match(literal, sprintf("%v", [v]))
}

declares contains [file, key] if {
	some file, doc in lib.workflows
	walk(doc, [_, v])
	is_string(v)
	some key, _ in pins
	regex.match(sprintf(`(?m)^\s*%s\s*[:=]\s*["']?[^$\s"']`, [key]), v)
}

deny contains msg if {
	some [file, key] in declares
	msg := sprintf("%s declares %s itself. versions.env owns that pin. Read it with `uses: ./.github/actions/versions` and refer to ${{ env.%s }} instead - a second declaration is a version that drifts silently.", [file, key, key])
}

exportable := `^[A-Z][A-Z0-9_]*_VERSION=[A-Za-z0-9][A-Za-z0-9._+-]*$`

deny contains msg if {
	some key, value in pins
	not regex.match(exportable, sprintf("%s=%v", [key, value]))
	msg := sprintf("versions.env declares %q, which .github/actions/versions will not export. A key must end in _VERSION, and a value must begin with a letter or digit and contain only letters, digits, dot, underscore, plus or dash. Anything else is skipped silently and the job runs with an empty value - see the action for why the pattern is deliberately this narrow. A checksum does not belong here at all: versions.env holds decisions, and a hash is what a supplier served for one. Declare the tool with fetch: or pypi: in scripts/approved-suppliers.yml and run task order-deliveries, which writes it to scripts/deliveries.lock (#416).", [key])
}

restricts(f) if {
	walk(f.contents, [_, v])
	is_string(v)
	contains(v, "_VERSION=[A-Za-z0-9][A-Za-z0-9._+-]*$")
}

deny contains msg if {
	msg := ".github/actions/versions no longer restricts what it writes to $GITHUB_ENV. On a fork's pull request versions.env is attacker-controlled, and an unconstrained write to the environment file is code execution in every later step - including the TruffleHog lane, which is the one lane deliberately not egress-blocked."
	some f in exporter
	not restricts(f)
}
