#!/usr/bin/env bash
# Decide whether the sensitive-path alarm was silenced by a human.
#
# The tripwire's forcing function is a label, and a label is only a forcing
# function if the party being gated cannot apply it. The agent's GitHub App
# holds `issues: write` - it needs that to comment on pull requests at all -
# and the same permission lets it label one. So the first version of this gate
# could be cleared by the agent, on the agent's own pull request, with no human
# involved: exactly the failure the whole mechanism exists to prevent.
#
# Checking that the label exists is therefore not enough. This asks who put it
# there, and refuses a bot.
#
# Usage, from the workflow:
#   PR=123 REPO=owner/name .github/scripts/acknowledged-by-human.sh
#
# Exit 0 when a human acknowledged, 1 otherwise. GH_TOKEN must be set.
set -euo pipefail

LABEL="${ACK_LABEL:-sensitive-reviewed}"
: "${PR:?PR number is required}"
: "${REPO:?REPO (owner/name) is required}"

# The timeline records every label event with the actor who caused it. The last
# one wins: a label removed and reapplied is acknowledged by whoever reapplied
# it, not by whoever touched it first.
actor=$(
	gh api --paginate "repos/${REPO}/issues/${PR}/timeline" \
		--jq "[.[] | select(.event == \"labeled\" and .label.name == \"${LABEL}\")] | last | .actor.login // empty"
)

if [ -z "$actor" ]; then
	echo "::error::This pull request changes a safety property. Read the comment above, then apply the '${LABEL}' label."
	exit 1
fi

# GitHub suffixes every App actor with [bot]. Matching the suffix rather than a
# name means a second App, or a renamed one, is refused too - the rule is "not
# a machine", not "not this particular machine".
case "$actor" in
*'[bot]')
	echo "::error::'${LABEL}' was applied by ${actor}, which is a bot. The acknowledgement has to come from a human - that is the entire point of it. Remove the label and re-apply it yourself."
	exit 1
	;;
esac

echo "acknowledged by ${actor}"
