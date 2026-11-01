#!/usr/bin/env bash
#
# What main is failing, as the outputs of pr-validation.yml's main-failing job.
#
# The ledger and the removal check run after a merge, because they take half an
# hour and are checks on checks (epoch 16, decisions 2 and 10). When one fails
# on main, every pull request runs it, on its own tree: one that clears it can
# land, which is how a fix gets in, and any other cannot. So nothing else has to
# stop merges, and nothing has to be told a pull request is a fix.
#
# Reads the newest finished run on main and ignores one that was cancelled,
# which says nothing. On main itself everything runs. A failure to read fails
# the job, and the gate with it: not knowing is not green.
#
# Prints, for $GITHUB_OUTPUT:
#   removal   true when the removal check should run
#   mutation  true when the whole ledger should run, not only what changed
#   parts     the ledger's jobs, as JSON
set -euo pipefail

whole=false
removal=false

api() {
	curl --proto '=https' -fsS \
		-H "Authorization: Bearer ${GITHUB_TOKEN}" \
		-H 'Accept: application/vnd.github+json' \
		"https://api.github.com/repos/${REPO}/actions/$1"
}

if [ "${EVENT}" = "push" ]; then
	whole=true
	removal=true
else
	runs="$(api 'workflows/pr-validation.yml/runs?event=push&branch=main&per_page=20')"
	run="$(jq -r 'first(.workflow_runs[] | select(.status == "completed" and .conclusion != "cancelled") | .id) // ""' <<<"${runs}")"
	if [ -n "${run}" ]; then
		jobs="$(api "runs/${run}/jobs?filter=latest&per_page=100")"
		failing="$(jq -r '.jobs[] | select(.conclusion == "failure" or .conclusion == "timed_out") | .name' <<<"${jobs}")"
		if grep -q '^test / mutation' <<<"${failing}"; then
			whole=true
			echo "main is failing the ledger, so this runs all of it" >&2
		fi
		if grep -q '^test / app removal$' <<<"${failing}"; then
			removal=true
			echo "main is failing the removal check, so this runs it" >&2
		fi
	fi
fi

parts='[1]'
if [ "${whole}" = true ]; then
	parts='[1, 2, 3, 4]'
fi

echo "mutation=${whole}"
echo "removal=${removal}"
echo "parts=${parts}"
