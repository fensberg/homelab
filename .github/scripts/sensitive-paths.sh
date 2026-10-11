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

# A proof that was removed has to be said to have been.
#
# Each proof that a guard fails when it should is a file of its own, so that
# adding one conflicts with nobody. What a count of them used to give - that
# one is not lost without somebody meaning it - is this: a changed file under
# the proofs' directory that is no longer there was removed, and a removal
# that the list of removals does not name, with a reason, stops here. It is
# refused and not reported, because a report is a thing a reviewer has to
# notice and this is a thing nobody has to.
PROOFS="${MUTATION_PROOFS_DIR:-tests/mutations}"
REMOVED="${MUTATION_PROOFS_REMOVED:-tests/mutations-removed.yml}"
unsaid=""
while IFS= read -r file; do
	case "$file" in
	"$PROOFS"/*.yml) ;;
	*) continue ;;
	esac
	if [ -e "$file" ]; then
		continue
	fi
	name="${file##*/}"
	# The entry naming it, and a reason on the line after.
	if ! grep -F -x -A1 -e "  - proof: ${name}" "$REMOVED" 2>/dev/null | grep -Eq '^    reason: +[^[:space:]]'; then
		unsaid="${unsaid}  ${file}
"
	fi
done <<PROOFS_EOF
$changed
PROOFS_EOF
if [ -n "$unsaid" ]; then
	cat >&2 <<PROOFS_EOF
This change removes proof(s) from the mutation ledger and does not say so:

${unsaid}
Each is the committed proof that a guard fails when it should. If it was
removed by mistake, put it back. If the guard no longer needs it, name it in
${REMOVED} with the reason, in the same change:

  - proof: <the file's name>
    reason: <why the guard no longer needs this proof>
PROOFS_EOF
	exit 1
fi
hits=""
matched=""
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
		# The matched files, one per line, for the caller. The attestation is a
		# review thread and a review thread has to hang off a file that is
		# actually in the diff - and the same list is what gets digested, so
		# the acknowledgement is bound to this content rather than to a moment.
		matched="${matched}${match}
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

# Deduplicated: one file can match two rules, and anchoring twice to the same
# file would open two threads asking for the same acknowledgement.
echo "files<<SENSITIVE_FILES_EOF"
printf '%s\n' "$matched" | grep -v '^$' | sort -u
echo "SENSITIVE_FILES_EOF"

echo "report<<SENSITIVE_PATHS_EOF"
cat <<EOF
<!-- sensitive-paths -->
## Sensitive paths changed — read before approving

${summary} Each line below says what breaks if this change is wrong.

${hits}---

**To merge:** resolve the review conversation opened on the file below. It is
anchored to the change itself, so resolving it means somebody was looking at the
diff rather than at a checkbox on the side of the page.

Whoever opened this pull request cannot resolve it. Approving and acknowledging
have to be two separate acts, by two different people - and a new conversation
opens whenever the sensitive part of this diff changes, so an acknowledgement
never outlives what it acknowledged.
EOF
echo "SENSITIVE_PATHS_EOF"
