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
RUN ./steamcmd.sh \
	+@sSteamCmdForcePlatformType linux \
	+force_install_dir /valheim \
	+login anonymous \
	+app_update "${VALHEIM_APP_ID}" validate \
	+quit

# --- run --------------------------------------------------------------------
FROM debian:bookworm-slim@sha256:88200866dfff7ea7f5cbcb6ec7c8a701889efe6fe859fe64d6990e4b07ea4171

# ca-certificates for the relay's TLS, libatomic1 because the server links
# against it. Nothing else: no shell tooling, no package manager left usable
# for anything, no SteamCMD.
# hadolint ignore=DL3008
RUN apt-get update \
	&& apt-get install --no-install-recommends -y \
		ca-certificates \
		libatomic1 \
	&& rm -rf /var/lib/apt/lists/*

# A real user, and one that owns nothing it does not need.
#
# The world lives on a mounted volume owned by this uid; the game files are
# root-owned and read-only to it. A server that cannot overwrite its own binary
# is one less thing an exploit can do with the process it lands in.
RUN useradd --system --uid 10000 --home-dir /valheim --shell /usr/sbin/nologin valheim
COPY --from=fetch --chown=root:root /valheim /valheim
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
