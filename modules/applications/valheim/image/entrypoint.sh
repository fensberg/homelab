#!/bin/sh
# Build the server's argument list from the environment.
#
# WHY THIS EXISTS, BECAUSE A WRAPPER SCRIPT IS USUALLY A SMELL.
#
# Kubernetes expands $(VAR) into exactly one argument and does not split on
# spaces, so a manifest cannot express "pass this flag only if it is set". That
# matters here because the server's own interface has no neutral value:
#
#   -modifier <key> <value>  every listed value CHANGES the game. There is no
#                            "normal" token, so an unset dial has to be an
#                            absent argument rather than a default one.
#   -setkey <flag>           presence-only. Passing `nomap` disables the map;
#                            there is no way to pass it meaning "off".
#
# Without this, every dial would have to be hardcoded in the manifest at a
# non-default value, or live as a comment nobody can set. Both were rejected.
#
# It adds a shell to the runtime image. That is a real cost and it is bounded:
# the script runs once, execs the server, and leaves nothing running that a
# compromise could use that a compromised server could not already do.
set -eu

# Required. Missing means a misconfigured Secret, and failing here names the
# field rather than letting the server start with an empty world name and
# create one called "".
for required in VALHEIM_SERVER_NAME VALHEIM_WORLD_NAME VALHEIM_PASSWORD; do
	eval "value=\${$required:-}"
	if [ -z "$value" ]; then
		echo "entrypoint: $required is empty - check the valheim-server Secret" >&2
		exit 78 # EX_CONFIG
	fi
done

set -- \
	-name "$VALHEIM_SERVER_NAME" \
	-world "$VALHEIM_WORLD_NAME" \
	-password "$VALHEIM_PASSWORD" \
	-savedir /data \
	-public "${VALHEIM_PUBLIC:-0}"

# The relay, and the reason nothing connects inbound. Flag-only: it takes no
# value, and its absence means the Steam backend and a port forward.
if [ "${VALHEIM_CROSSPLAY:-true}" = "true" ]; then
	set -- "$@" -crossplay
fi

# Everything below is optional, and empty means "leave it at the preset".
if [ -n "${VALHEIM_PRESET:-}" ]; then
	set -- "$@" -preset "$VALHEIM_PRESET"
fi

# The graded dials. Set one to override the preset on that axis alone.
for dial in combat deathpenalty resources raids portals; do
	name="VALHEIM_MODIFIER_$(echo "$dial" | tr '[:lower:]' '[:upper:]')"
	eval "value=\${$name:-}"
	if [ -n "$value" ]; then
		set -- "$@" -modifier "$dial" "$value"
	fi
done

# The on-or-off ones. Presence turns them on, so anything other than "true"
# leaves them alone rather than turning them off - the server has no way to
# express off.
for flag in nobuildcost playerevents passivemobs nomap; do
	name="VALHEIM_SETKEY_$(echo "$flag" | tr '[:lower:]' '[:upper:]')"
	eval "value=\${$name:-}"
	if [ "$value" = "true" ]; then
		set -- "$@" -setkey "$flag"
	fi
done

if [ -n "${VALHEIM_SAVE_INTERVAL:-}" ]; then
	set -- "$@" -saveinterval "$VALHEIM_SAVE_INTERVAL"
fi

# The arguments, minus the password, so a log shows what was asked for without
# showing the one thing that matters.
echo "entrypoint: starting with $(echo "$@" | sed 's/-password [^ ]*/-password ***/')" >&2

# The binary, with an override so the argument assembly above can be tested by
# something that runs this script rather than reads it.
#
# A seam rather than a setting: the default is the real server and nothing in
# the deployment sets it. Anyone who could set it in a running container can
# already replace the process by other means, so it widens nothing.
exec "${VALHEIM_SERVER_BINARY:-/valheim/valheim_server.x86_64}" "$@"
