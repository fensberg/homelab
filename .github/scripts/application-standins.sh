#!/usr/bin/env bash
# Print a stand-in for each value the platform hands a site's applications.
#
# Flux substitutes two kinds of value into what a site runs of an
# application, both from a secret OpenTofu writes: where releases are
# published (RELEASE_REPOSITORY, read by each release source in a site's
# applications file), and the address of each route an application declares
# (ADDRESS_<NAME>, set as the clusterIP of the Service of that name).
# Validation has no cluster and no secret, so it substitutes stand-ins.
#
# These are derived here rather than listed beside the others in
# tests/flux-substitutions.env, because that file is held to "every stand-in
# is used": a stand-in listed there for something only an application uses
# would have to be taken out when the last application was, and a route's
# would name the application's Service in a shared file. An estate with no
# application substitutes these into nothing, which is not a stand-in left
# behind.
#
# One line each, NAME=value, for a shell to read. A route's address is the
# host number it declares, in 192.0.2.0/24, which is the documentation range.
set -euo pipefail

echo "RELEASE_REPOSITORY=ghcr.io/example/homelab"
for declaration in modules/applications/*/application.json; do
	[ -e "$declaration" ] || continue
	jq -r '(.routes // {}) | to_entries[] | "ADDRESS_\(.key | ascii_upcase | gsub("-"; "_"))=192.0.2.\(.value)"' "$declaration"
done
