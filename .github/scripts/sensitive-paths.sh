#!/usr/bin/env bash
# Report which sensitive paths a change touches, and why they matter.
#
# Reads a list of changed files on stdin, matches them against
# .github/sensitive-paths, and writes GITHUB_OUTPUT-shaped lines to stdout so
# the caller can append them directly:
#
#     git diff --name-only "$BASE" "$HEAD" \
#       | .github/scripts/sensitive-paths.sh >>"$GITHUB_OUTPUT"
#
# This lives here rather than inline in the workflow for the reason everything
# else in this repository does: a script is reviewable, shellcheck lints it in
# the Shell Lint lane, and tests/go/repo/sensitivepaths_test.go runs it against
# known input. YAML embedded in a doc is none of those things - it is a copy
# that drifts from whatever is actually running.
#
# No third-party action, and no yq. The obvious dependency for this job is
# tj-actions/changed-files, which was backdoored in 2025 to scrape secrets out
# of runner memory into public logs. grep and parameter expansion have no
# supply chain.
set -euo pipefail

LIST="${SENSITIVE_PATHS_FILE:-.github/sensitive-paths}"

if [ ! -r "$LIST" ]; then
	echo "cannot read $LIST" >&2
	exit 1
fi

changed=$(cat)
hits=""
rules=0

while IFS= read -r line || [ -n "$line" ]; do
	path="${line%%#*}"
	path="$(printf '%s' "$path" | tr -d '[:space:]')"

	# Not `[ -z "$path" ] && continue`: that returns non-zero on the common
	# branch, which trips `set -e` and kills the run on the first real line.
	if [ -z "$path" ]; then
		continue
	fi

	why="${line#*#}"
	why="$(printf '%s' "$why" | sed 's/^[[:space:]]*//')"
	if [ "$why" = "$line" ]; then
		why="(no reason given)"
	fi

	# Anchored. An unanchored match means `vendor/tests/go/repo/x` trips the
	# alarm for `tests/go/repo/`. A trailing slash means the directory and
	# everything below it; no slash means that exact file.
	case "$path" in
	*/) match=$(printf '%s\n' "$changed" | grep -E "^${path}" || true) ;;
	*) match=$(printf '%s\n' "$changed" | grep -Fx "$path" || true) ;;
	esac

	if [ -n "$match" ]; then
		rules=$((rules + 1))
		# Rule and reason on one line, then the files under it. Leading with
		# the reason rather than the pattern is what makes the comment
		# actionable: the reviewer is told what breaks before being told
		# which glob matched.
		listed=""
		while IFS= read -r file; do
			if [ -n "$file" ]; then
				listed="${listed}- \`${file}\`
"
			fi
		done <<EOF
$match
EOF

		hits="${hits}**\`${path}\`** — ${why}

${listed}
"
	fi
done <"$LIST"

if [ -z "$hits" ]; then
	echo "tripped=false"
	exit 0
fi

# The whole comment body, not a fragment. Keeping the copy here rather than in
# the workflow means it can be edited by whoever maintains this file - the
# workflow is off-limits to the agent, and copy that needs a human to change is
# copy that stays wrong.
if [ "$rules" -eq 1 ]; then
	summary="**1 rule triggered.**"
else
	summary="**${rules} rules triggered.**"
fi

echo "tripped=true"
echo "report<<SENSITIVE_PATHS_EOF"
cat <<EOF
<!-- sensitive-paths -->
## Sensitive paths changed — read before approving

${summary} Each line below says what breaks if this change is wrong.

${hits}---

**To merge:** apply the \`sensitive-reviewed\` label. A bot cannot apply it — the
check reads who did, and refuses a machine. That is the point: approving and
acknowledging have to be two separate acts.
EOF
echo "SENSITIVE_PATHS_EOF"
