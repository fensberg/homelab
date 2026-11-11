#!/usr/bin/env bash
#
# What a revert of a stopped roll would be, as the outputs of
# deploy-infrastructure.yml's roll-aftermath job (#853).
#
# The machines' Talos version is TALOS_VERSION in scripts/versions.env, and the
# bump is the last commit that changed that line, not the last to touch the
# file, which moves with every other tool's pin. A revert puts that one line
# back and nothing else.
#
# Talos moves between patch versions in either direction and forward only
# across a minor one, so a revert is offered only when the roll touched a
# machine and both the version and the one before it are of the minor the
# machine image says it upgrades from.
#
# Reads TOUCHED, "true" when the roll moved a machine. Prints, for
# $GITHUB_OUTPUT:
#   sha         the commit that moved the version
#   version     the version the machines were moving to
#   previous    the version before it
#   revertible  true when a revert is offered
set -euo pipefail

versions=scripts/versions.env

fail() {
	echo "$1" >&2
	exit 1
}

version="$(sed -n 's/^TALOS_VERSION=//p' "$versions")"
[ -n "$version" ] || fail "no TALOS_VERSION in $versions, so no version can be named or put back"
# The minor version the machine image upgrades from, found by what declares
# it, not by which file it is in.
from="$(grep -rh --include='*.tf' 'talos_upgrades_from *= *"v' modules/infrastructure | sed -n 's/^ *talos_upgrades_from *= *"\(v[0-9.]*\)".*/\1/p' | sort -u)"
[ -n "$from" ] || fail "nothing under modules/infrastructure declares talos_upgrades_from, so whether this was a patch bump is not known"
[ "$(wc -l <<<"$from")" -eq 1 ] || fail "talos_upgrades_from is declared as more than one version under modules/infrastructure, so the minor is not known"

sha="$(git log -1 --format=%H -G '^TALOS_VERSION=' -- "$versions")"
[ -n "$sha" ] || fail "no commit changed TALOS_VERSION, so there is no bump to put back"
previous=""
if git rev-parse -q --verify "$sha^" >/dev/null; then
	previous="$(git show "$sha^:$versions" 2>/dev/null | sed -n 's/^TALOS_VERSION=//p')"
fi

revertible=false
if [ "${TOUCHED:-}" = true ] && [ -n "$previous" ] && [ "${version%.*}" = "$from" ] && [ "${previous%.*}" = "$from" ]; then
	revertible=true
fi

echo "sha=$sha"
echo "version=$version"
echo "previous=$previous"
echo "revertible=$revertible"
