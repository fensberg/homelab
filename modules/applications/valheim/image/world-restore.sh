#!/bin/sh
# Put the newest backup of the world in place, if the world is missing.
#
# Runs as an init container before the server, from the same image. Three
# outcomes, and only one of them starts the server on an empty world:
#
#   the world is on the volume     nothing to do - the volume is the truth
#   the bucket holds no backup     a genuinely new world; the server makes one
#   the bucket cannot be read      REFUSE. Starting fresh here would generate
#                                  a new world beside a backup that exists,
#                                  and the first backup after it would start
#                                  rotating the real one out.
#
# So an error reaching the bucket, or a restore that does not produce the
# world file, stops the pod rather than letting the server start.
set -eu

: "${VALHEIM_WORLD_NAME:?the world name comes from the valheim-server Secret}"
: "${WORLD_BACKUP_BUCKET:?the bucket comes from the valheim-backup Secret}"
: "${WORLD_BACKUP_KEY:?the key comes from the valheim-backup Secret}"

worlds="${WORLD_DIR:-/data/worlds_local}"
# The world is a directory, worlds_local/<world>/, in Valheim 1.0's chunked
# format: region files (*.chunk) and, per save generation, a manifest
# (_main.<n>.db2, .fwl2, .chunks) closed by _main.<n>.ok, which the server
# writes last. So a world is present when a completed save is - an .ok marker.
#
# Read off the production volume. Two earlier versions guessed instead - a loose
# <world>.db, then a <world>/*.db - and each found no world on a volume that had
# one; they were harmless only because no backup existed to restore over it.
world="${worlds}/${VALHEIM_WORLD_NAME}"

has_world() {
	find "$world" -maxdepth 1 -name '_main.*.ok' 2>/dev/null | grep -q .
}

if has_world; then
	echo "world-restore: ${world} is present, so there is nothing to restore"
	exit 0
fi

export RCLONE_CONFIG_WORLD_TYPE=crypt
export RCLONE_CONFIG_WORLD_REMOTE="r2:${WORLD_BACKUP_BUCKET}/worlds/${VALHEIM_WORLD_NAME}"
export RCLONE_CONFIG_WORLD_FILENAME_ENCRYPTION=off
export RCLONE_CONFIG_WORLD_DIRECTORY_NAME_ENCRYPTION=false
RCLONE_CONFIG_WORLD_PASSWORD="$(rclone obscure "$WORLD_BACKUP_KEY")"
export RCLONE_CONFIG_WORLD_PASSWORD

if ! listing="$(rclone lsf --dirs-only world:)"; then
	echo "world-restore: could not list the backups, so refusing to start a new world over one that may exist" >&2
	exit 1
fi
latest="$(printf '%s\n' "$listing" | grep 'Z/$' | sort | tail -n 1 || true)"
latest="${latest%/}"

if [ -z "$latest" ]; then
	echo "world-restore: no backup of ${VALHEIM_WORLD_NAME} exists, so the server will generate a new world"
	exit 0
fi

mkdir -p "$world"
rclone copy "world:${latest}" "$world"
if ! has_world; then
	echo "world-restore: restored ${latest}, but ${world} is not there - refusing to start" >&2
	exit 1
fi
echo "world-restore: restored ${VALHEIM_WORLD_NAME} from ${latest}"
