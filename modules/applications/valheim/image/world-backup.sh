#!/bin/sh
# Back the world up to the production bucket, encrypted, every hour.
#
# Runs as a sidecar beside the server, from the same image. Each pass copies the
# world's directory - worlds_local/<world>/, whatever the server keeps in it -
# into a directory named for the UTC time, then removes all but the newest
# WORLD_BACKUP_KEEP. A directory, not a pair of loose files: that is where this
# server keeps a world, and the first version of this script looked for
# worlds_local/<world>.db, found nothing, and backed up nothing. The bucket holds ciphertext: the files go
# through an rclone crypt remote keyed from WORLD_BACKUP_KEY, and the key lives
# in 1Password, not beside the bucket.
#
# A pass that fails exits non-zero, so the sidecar restarts and its restart
# count says so. A backup that fails quietly is the one nobody finds out about
# until the day it is needed.
set -eu

: "${VALHEIM_WORLD_NAME:?the world name comes from the valheim-server Secret}"
: "${WORLD_BACKUP_BUCKET:?the bucket comes from the valheim-backup Secret}"
: "${WORLD_BACKUP_KEY:?the key comes from the valheim-backup Secret}"

interval="${WORLD_BACKUP_INTERVAL:-3600}"
keep="${WORLD_BACKUP_KEEP:-48}"
worlds="${WORLD_DIR:-/data/worlds_local}"
world="${worlds}/${VALHEIM_WORLD_NAME}"

export RCLONE_CONFIG_WORLD_TYPE=crypt
export RCLONE_CONFIG_WORLD_REMOTE="r2:${WORLD_BACKUP_BUCKET}/worlds/${VALHEIM_WORLD_NAME}"
export RCLONE_CONFIG_WORLD_FILENAME_ENCRYPTION=off
export RCLONE_CONFIG_WORLD_DIRECTORY_NAME_ENCRYPTION=false
RCLONE_CONFIG_WORLD_PASSWORD="$(rclone obscure "$WORLD_BACKUP_KEY")"
export RCLONE_CONFIG_WORLD_PASSWORD

while :; do
	if find "$world" -maxdepth 1 -name '*.db' 2>/dev/null | grep -q .; then
		stamp="$(date -u +%Y%m%dT%H%M%SZ)"
		rclone copy "$world" "world:${stamp}"
		echo "world-backup: backed up ${VALHEIM_WORLD_NAME} as ${stamp}"

		listing="$(rclone lsf --dirs-only world:)"
		count="$(printf '%s\n' "$listing" | grep -c 'Z/$' || true)"
		if [ "$count" -gt "$keep" ]; then
			printf '%s\n' "$listing" | grep 'Z/$' | sort | head -n "$((count - keep))" |
				while IFS= read -r old; do
					rclone purge "world:${old%/}"
					echo "world-backup: removed ${old%/}, keeping the newest ${keep}"
				done
		fi
	else
		echo "world-backup: no world in ${world} yet, so nothing to back up"
	fi
	if [ "${WORLD_BACKUP_ONCE:-}" = "1" ]; then
		exit 0
	fi
	sleep "$interval"
done
