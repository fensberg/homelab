#!/usr/bin/env bash
# Render config/management.tpl.json with placeholders instead of secrets.
#
# `tofu validate` reads the rendered config through jsondecode(file(...)), so it
# cannot run without one - and CI has no 1Password credentials, deliberately.
# This writes a stand-in with the same shape and no real values.
#
# It is not only a means to an end. If the template stops being valid JSON once
# injected, or stops carrying a key the OpenTofu reads, validate says so on the
# pull request rather than on the night of a deployment.
set -euo pipefail

template="config/management.tpl.json"

# NOT config/management.rendered.json. That filename belongs to the Render
# phase, and an ignition run in another terminal is reading it - `task
# validate` used to write and then delete it, which sabotaged a live
# deployment mid-flight and left its state describing VMs it could no longer
# see. A separate filename means validate and a real run cannot collide.
rendered="config/management.placeholder.json"

# Two passes, and the order matters.
#
# Some values have to be shape-correct or a provider rejects them while
# validate is still evaluating its configuration block - the flux provider
# checks that the repository URL has a scheme it recognises before anything
# else happens. private_key is the same class of problem for a different
# reason: it reaches a Kubernetes secret's binary_data, which must be valid
# base64, and the literal string "placeholder" is not. So URL-shaped keys and
# the key are replaced first, by name, and the second pass sweeps up
# everything left.
sed -E \
	-e 's#"([a-z_]*_url|url|endpoint)": *"\{\{[^}]*\}\}"#"\1": "https://example.invalid/placeholder"#' \
	-e 's#"(private_key)": *"\{\{[^}]*\}\}"#"\1": "cGxhY2Vob2xkZXI="#' \
	-e 's#\{\{[^}]*\}\}#placeholder#g' \
	"${template}" >"${rendered}"

if grep -q '{{' "${rendered}"; then
	echo "unresolved template markers remain in ${rendered}:" >&2
	grep -n '{{' "${rendered}" >&2
	exit 1
fi

# A bare op:// reference, written without the {{ }}, survives both passes above
# and every schema check after them - `op inject` would not substitute it
# either, so the literal reference string reaches whatever reads that key. The
# hermetic version of this check lives in tests/go/repo; this is the backstop
# for anything that renders the template without running the test suite.
if grep -q 'op://' "${rendered}"; then
	echo "unwrapped op:// reference(s) in ${template} - wrap them in {{ }}:" >&2
	grep -n 'op://' "${rendered}" >&2
	exit 1
fi

echo "wrote ${rendered}"
