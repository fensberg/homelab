# Valheim

The dedicated server, with the world backed up to object storage and restored
from it. Everything about it is in this directory; the estate learns what it
needs from [`application.json`](application.json).

## What it declares, and why

**`routes`.** `game-server` is the Service enrolled devices reach on UDP 2456
and 2457 (#446). It is named for what it is rather than for the application:
the namespace already says which application, so a Service called the same
would read `valheim.valheim` and tell a reader nothing.

**`secrets`.** Two, both read from the vault item `<site>/valheim`:

- `valheim-server` - the server's name in the listing, the world's name, and
  the password. Keeping all three in one Secret means the deployment reads
  them one way; a pod half-configured from a ConfigMap and half from a Secret
  is two things to check when the wrong world loads.
  - `name` is the world. **Changing it starts a new world**: the save is
    looked up by this name, so a different value is not a rename. The old
    world stays on the volume, untouched and unloaded, and an empty one is
    generated beside it.
  - `server_name` is what the listing shows. It is a separate field because
    the two are not one thing: the world name is welded to a file on disk and
    the listing name is free. They were one field once, and the only way to
    change the listing was to abandon the world.
  - Neither name is a credential - the listing name is broadcast to anyone
    who joins. They are in the vault for the reason site names are: a fork
    picks its own by changing its vault rather than a manifest.
- `valheim-backup` - what the backup sidecar and the restore init container
  need, and nothing else in the pod mounts: the bucket of the environment the
  site runs this in, a credential scoped to that bucket alone, and the key the
  backups are encrypted with. rclone is configured entirely from the
  environment: the remote `r2` is the bucket, and the scripts wrap it in an
  encrypting remote keyed from `WORLD_BACKUP_KEY`, so the bucket holds
  ciphertext. `backup_key` is `generated`: nobody types it, so a rebuilt site
  restores the world with no human in the loop.

**`release`.** The version is read from the line the server prints at start,
`Valheim version: l-1.0.15 (network version 40)`; the declaration carries that
line as the proof its pattern reads one.

**`upstream`.** Steam. Clients update themselves and refuse a server on an
older build, so the expediter records Valve's new build on the
`VALHEIM_STEAM_BUILD_VERSION` line of [`pins.env`](pins.env), which is the one
line its standing order covers. `app` is the dedicated server, whose public
build is the one to run; `news` is the client, because that is where Valve
posts patch notes.

**`before_teardown`.** One run of the backup, in the sidecar, before a site
is destroyed (#588).

## What is tested here

[`tests/`](tests/) is this application's own: the entrypoint's arguments, the
backup and restore scripts against a fake bucket, the pod's shape, and that
the network policy still lets players on ordinary networks get a direct path.
[`tests/mutations.yml`](tests/mutations.yml) proves each of them fails when
what it guards is broken. The repository's guards run both.
