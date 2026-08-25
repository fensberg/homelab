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
rendered="config/management.rendered.json"

# Two passes, and the order matters.
#
# Some values have to be shape-correct or a provider rejects them while
# validate is still evaluating its configuration block - the flux provider
# checks that the repository URL has a scheme it recognises before anything
# else happens. So URL-shaped keys are replaced first, by name, and the second
# pass sweeps up everything left.
sed -E \
	-e 's#"([a-z_]*_url|url|endpoint)": *"\{\{[^}]*\}\}"#"\1": "https://example.invalid/placeholder"#' \
	-e 's#\{\{[^}]*\}\}#placeholder#g' \
	"${template}" >"${rendered}"

if grep -q '{{' "${rendered}"; then
	echo "unresolved template markers remain in ${rendered}:" >&2
	grep -n '{{' "${rendered}" >&2
	exit 1
fi

echo "wrote ${rendered}"
