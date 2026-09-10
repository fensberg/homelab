# The game server this estate hosts, built rather than borrowed.
#
# There is no official container image for a Valheim dedicated server, and
# there are several popular community ones. This estate does not take an
# unvetted image - popularity is not provenance - and it already has the
# machinery to build its own: a committed Dockerfile, versions from
# scripts/versions.env, published to the organisation's registry and pinned by
# digest at its point of use. The runner image set that pattern.
#
# Suppliers: Docker Official Images for the base, and Valve for SteamCMD and
# the server itself. Both approved on their own, deliberately - see the entries
# in scripts/approved-suppliers.yml for what each delivers and what bounds it.

# --- fetch ------------------------------------------------------------------
#
# A separate stage so SteamCMD does not ship in the running image. It is a
# build tool: it authenticates, downloads depots and exits, and none of that is
# something a game server should be able to do at runtime.
FROM debian:bookworm-slim@sha256:88200866dfff7ea7f5cbcb6ec7c8a701889efe6fe859fe64d6990e4b07ea4171 AS fetch

# lib32gcc-s1 is not optional: SteamCMD is a 32-bit binary, on a 64-bit base.
# It fails with a bare "No such file or directory" without it, which reads as a
# missing download rather than a missing library.
#
# hadolint's DL3008 wants these pinned to exact versions, and the runner image
# gives the reason not to: Debian's archive keeps only the current version of a
# package, so a pin stops existing the moment a security update supersedes it
# and the build fails outright - and until it does, the pin holds the image on
# a version deliberately not receiving those updates. What decides what this
# image actually runs is the base digest above and the server fetched below.
# hadolint ignore=DL3008
RUN apt-get update \
	&& apt-get install --no-install-recommends -y \
		ca-certificates \
		curl \
		lib32gcc-s1 \
	&& rm -rf /var/lib/apt/lists/*

WORKDIR /steamcmd
# Downloaded and then extracted rather than piped. A pipe hides curl's exit
# status behind tar's - a failed download feeding a working extractor is a
# successful step - and the alternative is setting a shell that tolerates
# pipes, which fixes the symptom rather than removing it.
RUN curl -fsSL -o steamcmd.tar.gz https://media.steampowered.com/client/steamcmd_linux.tar.gz \
	&& tar -xzf steamcmd.tar.gz \
	&& rm steamcmd.tar.gz

# THE GAME VERSION IS DELIBERATELY NOT PINNED, AND THAT IS NOT AN OVERSIGHT.
#
# This estate pins everything, so the exception needs its reason stated where
# somebody will read it before "fixing" it.
#
# Valheim clients update themselves through Steam and refuse to join a server
# on a different version. A pinned server would therefore work until the next
# game update and then be unreachable by every player - the pin would cause the
# outage rather than prevent one.
#
# What IS pinned is this image: built at a point in time, published to the
# estate's registry, referenced by digest. So the deployed bytes are exact and
# reviewable; what floats is only which upstream build a *new* image contains.
# Updating the game is rebuilding this image, which is a reviewed change like
# any other.
#
# 896660 is the Valheim Dedicated Server, and it installs under an anonymous
# login. If Valve ever withdraws anonymous access - reported once and since
# resolved - this fails loudly here at build time rather than at runtime, which
# is the right place for it to fail.
ARG VALHEIM_APP_ID=896660

# Retried, because SteamCMD fails this way intermittently and the failure is
# not about this image.
#
# On its first invocation SteamCMD updates itself and restarts - "Restarting
# steamcmd by request..." - and an app_update issued in that same run comes
# back with
#
#   ERROR! Failed to install app '896660' (Missing configuration)
#
# and exit code 8. The same Dockerfile built successfully twice earlier the same
# evening and then failed twice in a row, with nothing here changed between
# them. So the warm-up run below gets the self-update out of the way first, and
# the download is then attempted up to five times.
#
# Two more things go with the retry, because the failure recurred identically
# rather than clearing, which is not what a transient flake looks like.
#
# The steamapps directory is created first. SteamCMD downloads and applies an
# update to ITSELF on every run - forty megabytes of it, visible in the build
# log - so the client is not a fixed input the way the base image is. A client
# released between two builds is the only thing that changed here, and a
# missing steamapps under force_install_dir is a known way for a newer client
# to answer "Missing configuration" against a directory it is creating fresh.
#
# app_info_update 1 forces the app metadata to be refetched rather than read
# from whatever the freshly-installed client cached. "Missing configuration" is
# a complaint about exactly that metadata, so refusing to trust the cache is
# aimed straight at it.
#
# Neither is certain. Both are one line, harmless when unnecessary, and target
# the specific error - which beats another twenty-minute cycle to test them one
# at a time.
#
# The trailing test is not decoration. SteamCMD is known to exit 0 having
# downloaded nothing, and this is the last moment anything can tell - after
# this stage the files are copied into an image that would build cleanly,
# publish, deploy, and fail at runtime with the server binary simply absent.
RUN set -eu; \
	./steamcmd.sh +quit || true; \
	mkdir -p /valheim/steamapps; \
	ok=0; \
	for attempt in 1 2 3 4 5; do \
		if ./steamcmd.sh \
			+@sSteamCmdForcePlatformType linux \
			+force_install_dir /valheim \
			+login anonymous \
			+app_info_update 1 \
			+app_update "${VALHEIM_APP_ID}" validate \
			+quit; then ok=1; break; fi; \
		echo "steamcmd attempt ${attempt} failed; retrying in 15s" >&2; \
		sleep 15; \
	done; \
	if [ "$ok" != 1 ]; then \
		echo "steamcmd could not install app ${VALHEIM_APP_ID} after 5 attempts" >&2; \
		exit 1; \
	fi; \
	if [ ! -f /valheim/valheim_server.x86_64 ]; then \
		echo "steamcmd reported success but the server binary is not there" >&2; \
		exit 1; \
	fi

# --- run --------------------------------------------------------------------
FROM debian:bookworm-slim@sha256:88200866dfff7ea7f5cbcb6ec7c8a701889efe6fe859fe64d6990e4b07ea4171

# ca-certificates for the relay's TLS, libatomic1 because the server links
# against it, and PulseAudio because PlayFab Party does.
#
# THE PULSEAUDIO PACKAGES ARE NOT A MISTAKE AND ARE NOT OPTIONAL. Nothing here
# plays or records audio, and a headless dedicated server has no sound device
# at all - but PlayFab Party is a combined voice-and-data SDK shipped as one
# native library, and `libparty.so` links PulseAudio whether or not voice is
# used. Without them the library cannot load.
#
# What that failure looks like matters, because it does not look like a missing
# library. The server starts, generates its world, and logs in to PlayFab
# perfectly happily - all of that is managed code over HTTPS. Only the relay
# network needs the native runtime, so the symptom is an EMPTY JOIN CODE and a
# thirty-second loop, with no error naming a library anywhere in it:
#
#   New session server "..." that has join code ,  now 0 player(s)
#   Server '...' begin PlayFab create and join network for server
#   [30s] PlayFab reconnect server '...'
#
# libpulse0 carries libpulse.so.0 and libpulse-simple.so.0;
# libpulse-mainloop-glib0 carries the third. All three are required.
#
# Not installed, deliberately: the wayland, cairo, pango and dbus libraries
# that `libdecor-0.so.0` and `libdecor-cairo.so` also report as missing. Those
# are Unity's desktop windowing stack, shipped in every build of the engine and
# never loaded by a headless server. Installing them would be several megabytes
# of attack surface bought to silence a scan that is correctly reporting
# something harmless.
# hadolint ignore=DL3008
RUN apt-get update \
	&& apt-get install --no-install-recommends -y \
		ca-certificates \
		libatomic1 \
		libpulse-mainloop-glib0 \
		libpulse0 \
	&& rm -rf /var/lib/apt/lists/*

# A real user, and one that owns nothing it does not need.
#
# The world lives on a mounted volume owned by this uid; the game files are
# root-owned and read-only to it. A server that cannot overwrite its own binary
# is one less thing an exploit can do with the process it lands in.
RUN useradd --system --uid 10000 --home-dir /valheim --shell /usr/sbin/nologin valheim
COPY --from=fetch --chown=root:root /valheim /valheim

# Prove the relay's library can actually load, here, at build time.
#
# This is the guard for the packages above, and it is a build step rather than
# a test because nothing a test can reach knows what a Debian image resolves at
# runtime. It earns its place by the shape of the failure it catches: a missing
# dependency here produces an image that starts, runs, and serves a world that
# nobody can join, retrying every thirty seconds forever with no error naming a
# library. Hours of looking at network policy and relay documentation, for a
# package that was never installed.
#
# So the build fails instead, at the one step that could fix it, naming the
# libraries by name. If a future Valheim update moves or renames this file the
# build also fails, which is the right outcome - it is the estate's one link to
# the crossplay backend and its absence is worth knowing about immediately.
RUN set -eu; \
	party=/valheim/valheim_server_Data/Plugins/libparty.so; \
	if [ ! -f "$party" ]; then \
		echo "libparty.so is not where this image expects it: $party" >&2; \
		echo "the PlayFab relay is what crossplay runs on - find where it moved" >&2; \
		exit 1; \
	fi; \
	missing="$(ldd "$party" | grep 'not found' || true)"; \
	if [ -n "$missing" ]; then \
		echo "the PlayFab Party runtime cannot load - unresolved dependencies:" >&2; \
		echo "$missing" >&2; \
		echo "add the packages providing them to the apt-get above" >&2; \
		exit 1; \
	fi

RUN mkdir -p /data && chown valheim:valheim /data

USER 10000
WORKDIR /valheim

# The server finds its bundled libraries here rather than in the system paths.
ENV LD_LIBRARY_PATH=/valheim/linux64
# Steam's own telemetry, off. It is on by default and this estate does not send
# what it does not have to.
ENV SteamAppId=892970

# The world, on the volume rather than in the image. -savedir moves it off the
# server's default under the home directory, so nothing depends on where a
# container's home happens to be.
VOLUME ["/data"]

# No CMD arguments here on purpose.
#
# Every one of them - the server name, the world, the password, the difficulty -
# is either environment-specific or a secret, and this file is neither. The
# deployment supplies them as environment variables and the entrypoint turns
# them into the server's argument list.
#
# That indirection earns its place: the server has no neutral value for a world
# modifier and no way to express "off" for a setkey flag, so an unset dial has
# to be an ABSENT argument. A manifest cannot express that, because Kubernetes
# expands $(VAR) into exactly one argument and never splits it.
COPY --chown=root:root entrypoint.sh /usr/local/bin/entrypoint.sh
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
