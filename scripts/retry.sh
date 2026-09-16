#!/usr/bin/env bash
# Run a command again when it fails for a reason that is not the command's.
#
# WHAT THIS IS FOR, AND THE LINE IT MUST NOT CROSS.
#
# A dependency fetch can fail for reasons that have nothing to do with the
# change being checked. The Validate lane failed on:
#
#   Error while installing hashicorp/kubernetes v2.38.0: could not query
#   provider registry ... 500 Internal Server Error returned from
#   https://github.com/opentofu/.../terraform-provider-kubernetes_2.38.0_SHA256SUMS
#
# That is GitHub's release CDN having a bad minute. Nothing in the pull request
# could cause it and nothing in the pull request can fix it.
#
# So this wraps a FETCH, never a VERDICT. `tofu init` downloads pinned
# providers and is idempotent; `tofu validate`, `tofu test`, `go test` and
# every guard in this repository answer a question about the change, and
# retrying one of those until it passes is how a real failure becomes a flake
# nobody investigates. The caller decides which it is, because this script
# cannot tell - and that is the whole reason the rule is written here rather
# than assumed.
#
# The same split already exists twice in this estate and is the model:
# scripts/clerk/llm.go retries a 429 or a 5xx and returns immediately on any
# other 4xx, because "a rejected key is rejected on the third attempt too"; and
# expediter.yml bounds its Steam lookup at three attempts and then fails
# loudly.
#
# BOUNDED, ALWAYS. An unbounded retry against a metered vendor is a bill
# nobody sees coming, which this estate treats as a blast radius alongside
# security. Attempts and delay are arguments rather than defaults buried here,
# so a caller that wants more has to say so where somebody reviews it.
#
# EVERY ATTEMPT IS REPORTED. A retry that quietly succeeded is a dependency
# degrading invisibly: if a fetch needs three attempts every single run, that
# is a finding, and silence would turn it into the new normal. The line goes to
# stderr so it survives a caller capturing stdout, and to the job summary when
# there is one.
#
# Usage:
#   scripts/retry.sh <attempts> <delay-seconds> <command> [args...]
set -euo pipefail

if [ "$#" -lt 3 ]; then
	echo "usage: $0 <attempts> <delay-seconds> <command> [args...]" >&2
	exit 2
fi

attempts="$1"
delay="$2"
shift 2

case "$attempts" in
'' | *[!0-9]*)
	echo "retry: attempts must be a whole number, got '${attempts}'" >&2
	exit 2
	;;
esac
case "$delay" in
'' | *[!0-9]*)
	echo "retry: delay must be a whole number of seconds, got '${delay}'" >&2
	exit 2
	;;
esac
if [ "$attempts" -lt 1 ]; then
	echo "retry: attempts must be at least 1" >&2
	exit 2
fi

# A ceiling on the ceiling. Somebody reaching for a large number is reaching
# for the wrong tool - a fetch that needs twenty attempts is a broken
# dependency, and waiting for it in CI converts a clear failure into a slow one.
readonly MAX_ATTEMPTS=5
if [ "$attempts" -gt "$MAX_ATTEMPTS" ]; then
	echo "retry: ${attempts} attempts is more than the ${MAX_ATTEMPTS} this allows." >&2
	echo "       Something needing more than that is not flaky, it is broken - and" >&2
	echo "       waiting on it in CI turns a clear failure into a slow one." >&2
	exit 2
fi

# WHAT MAY BE RETRIED, declared here and refused everywhere else.
#
# This list is the whole mechanism, and it lives in the SCRIPT rather than in a
# test on purpose. A test is a thing somebody has to remember to extend; this
# refuses at the moment of use, for every caller that exists now and every one
# written later, whether or not anybody thought to add a guard for it.
#
# An entry is a command whose failure has nothing to do with the change being
# checked and which the change cannot fix: a dependency fetch, a registry
# lookup, a vendor API that answers a question about the outside world.
#
# Everything else is a VERDICT - it answers a question about the change - and
# retrying a verdict until it passes is how a real failure becomes a flake
# nobody investigates. The run goes green either way, so nothing would ever say
# which it was. That is why the default is refusal rather than permission.
#
# Adding a line here is a deliberate act in a reviewed file. Getting it wrong
# is not silent: the command is refused with this paragraph.
readonly RETRYABLE=(
	"tofu init"      # downloads pinned providers; idempotent, and the failure is the registry's
	"terraform init" # same, for anyone running the other one
	"steamcmd.sh"    # Valve's app_info lookup, which reports no build under load
	"curl"           # a fetch, by definition
	"npm ci"         # a registry install
	"pnpm install"   # a registry install
	"go mod download"
	"ansible-galaxy"
)

is_retryable() {
	local invocation="$*"
	local allowed
	for allowed in "${RETRYABLE[@]}"; do
		case "$invocation" in
		*"$allowed"*) return 0 ;;
		esac
	done
	return 1
}

if ! is_retryable "$@"; then
	{
		echo "retry: refusing to retry \`$*\`."
		echo
		echo "Only a FETCH may be retried - a command whose failure has nothing to do"
		echo "with the change being checked and which the change cannot fix. Everything"
		echo "else answers a question ABOUT the change, and retrying one of those until"
		echo "it passes turns a real failure into a flake nobody investigates: the run"
		echo "goes green either way, so nothing ever says which it was."
		echo
		echo "If this genuinely is a fetch, add it to RETRYABLE in scripts/retry.sh and"
		echo "say in one line why its failure is not the change's fault."
	} >&2
	exit 2
fi

say() {
	echo "$1" >&2
	if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
		echo "$1" >>"$GITHUB_STEP_SUMMARY"
	fi
}

for attempt in $(seq 1 "$attempts"); do
	if "$@"; then
		if [ "$attempt" -gt 1 ]; then
			# Said out loud on success, not only on failure. This is the line
			# that makes a degrading dependency visible.
			say "retry: \`$1\` succeeded on attempt ${attempt} of ${attempts}. It failed $((attempt - 1)) time(s) first, which is worth knowing even though this run is green."
		fi
		exit 0
	fi
	if [ "$attempt" -lt "$attempts" ]; then
		say "retry: \`$1\` failed on attempt ${attempt} of ${attempts}; waiting ${delay}s"
		sleep "$delay"
	fi
done

say "retry: \`$1\` failed ${attempts} time(s) and is not being retried again."
exit 1
