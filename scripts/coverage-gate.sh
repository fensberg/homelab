#!/usr/bin/env bash
# Report a coverage number, and fail only if it has gone backwards.
#
#   scripts/coverage-gate.sh go 24.5
#   scripts/coverage-gate.sh js 81.2
#
# A fixed threshold was considered and rejected. This repository has real code
# that predates its tests, so any threshold high enough to be worth having
# would block the very pull requests that add the missing tests, and any
# threshold low enough not to would not be enforcing anything. A ratchet has
# neither problem: it cannot be satisfied by deleting tests, it never blocks
# work that leaves coverage where it found it, and raising the floor is a
# deliberate committed change rather than a number that drifts.
#
# The baseline lives in tests/coverage-baseline.json, in git, so a change to
# it is reviewable on the pull request that earns it.
set -euo pipefail

BASELINE_FILE="${BASELINE_FILE:-tests/coverage-baseline.json}"

# Coverage tooling reports one decimal place and can wobble in the last one
# between runs on identical code (statement ordering, a flaky-free but
# differently-scheduled parallel test). Half a point of slack absorbs that
# without absorbing a real regression, which is always far larger.
TOLERANCE="${COVERAGE_TOLERANCE:-0.5}"

usage() {
	echo "usage: $0 <language> <current-percentage>" >&2
	echo "example: $0 go 24.5" >&2
	exit 2
}

[ "$#" -eq 2 ] || usage
language="$1"
current="$2"

if ! [[ $current =~ ^[0-9]+(\.[0-9]+)?$ ]]; then
	echo "coverage-gate: '$current' is not a percentage" >&2
	exit 2
fi

if [ ! -f "$BASELINE_FILE" ]; then
	echo "coverage-gate: no baseline at $BASELINE_FILE" >&2
	exit 2
fi

baseline="$(jq -r --arg lang "$language" '.baselines[$lang] // empty' "$BASELINE_FILE")"
if [ -z "$baseline" ]; then
	echo "coverage-gate: $BASELINE_FILE declares no baseline for '$language'" >&2
	echo "known languages: $(jq -r '.baselines | keys | join(", ")' "$BASELINE_FILE")" >&2
	exit 2
fi

# awk rather than bash arithmetic, which is integer-only.
floor="$(awk -v b="$baseline" -v t="$TOLERANCE" 'BEGIN { printf "%.2f", b - t }')"
verdict="$(awk -v c="$current" -v f="$floor" -v b="$baseline" -v t="$TOLERANCE" 'BEGIN {
	if (c + 0 < f + 0)      { print "regressed" }
	else if (c + 0 > b + t) { print "improved" }
	else                    { print "held" }
}')"

emoji=""
note=""
case "$verdict" in
regressed)
	emoji="🔴"
	note="**below the committed baseline.** Add tests for what this change touched, or - if the drop is genuinely correct, e.g. dead code was replaced by more code - lower the baseline in \`$BASELINE_FILE\` in this same pull request and say why in the message."
	;;
improved)
	emoji="🟢"
	note="above the baseline. Raise it to \`$current\` in \`$BASELINE_FILE\` to lock the gain in - the ratchet only holds ground that has been committed."
	;;
held)
	emoji="⚪"
	note="holding at the baseline."
	;;
esac

summary=$(
	cat <<-EOF
		### Coverage: ${language}

		| | |
		| --- | --- |
		| This run | \`${current}%\` |
		| Baseline | \`${baseline}%\` |
		| Floor (baseline − ${TOLERANCE}) | \`${floor}%\` |

		${emoji} ${note}
	EOF
)

echo "$summary"
if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
	echo "$summary" >>"$GITHUB_STEP_SUMMARY"
fi

if [ "$verdict" = "regressed" ]; then
	echo "coverage-gate: ${language} coverage ${current}% is below the floor of ${floor}%" >&2
	exit 1
fi
