# The sweeper, as it runs in the cluster: the one program and nothing else.
#
# It is built here from this repository's own source, which the work order
# names as what the build reads (scripts/work-orders.json). The source is
# mounted for the one step that compiles it and is in no layer; the image
# holds the compiled program alone, on an empty base, as the account
# Kubernetes' restricted profile asks for.
#
# What it runs is `sweeper scrub-untrusted`, which puts a used untrusted
# worker back as it was built (scripts/sweeper/scrub-untrusted.go).
ARG GO_VERSION

# The toolchain, pinned by digest, as .github/pull-request-runner-image
# /Dockerfile pins the same one; see that file for how to move it. The build
# asks it what version it is, so a digest left behind by its version stops
# the build rather than compiling with the old one under the new name.
FROM golang:${GO_VERSION}-bookworm@sha256:e8c859f5632dcfde7b32d2012b4351728f6437930887c2f6a91ea242459e5514 AS build
ARG GO_VERSION

# A pipe's failure is the build's failure.
SHELL ["/bin/bash", "-o", "pipefail", "-c"]

# Static, so the empty base below can run it; with no record of where it was
# built, so two builds of one source are one program; and with the toolchain
# it was given, never one fetched for it.
RUN --mount=type=bind,from=reads,target=/src \
    go version | grep -F "go${GO_VERSION} " \
    && CGO_ENABLED=0 GOTOOLCHAIN=local go build -C /src/scripts/sweeper \
        -trimpath -buildvcs=false -o /out/sweeper .

FROM scratch
COPY --from=build /out/sweeper /sweeper

# Numeric, for the reason .github/runner-image/Dockerfile gives: a kubelet
# asked for runAsNonRoot cannot resolve a name, and there are no names here.
USER 65532:65532
ENTRYPOINT ["/sweeper", "scrub-untrusted"]
