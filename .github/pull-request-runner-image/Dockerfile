# The image a pull request's jobs run in: a runner with its tools already in
# it, so that a job starts on its work rather than on its installs.
#
# It is not the image a converge runs in (.github/runner-image). That one
# carries what reaches the estate - the vault's client, the cluster's, the
# machines' - and this one carries none of them, because what runs here is
# whatever a pull request says to run. What is here is what the checks use:
# Go, Node, and the tools scripts/deliveries.lock holds.
#
# Nothing in a job changes to use it. The steps that install these tools stay
# where they are and find them here:
#
#   - Go and Node sit in the runner's tool cache, at the versions
#     scripts/versions.env pins, where setup-go and setup-node look before
#     they download anything.
#   - The locked tools are installed by scripts/take-delivery.sh itself, which
#     records what it delivered and does not fetch it twice.
#
# So a job that asks for a version this image was not built with gets that
# version the slow way, exactly as it does on any other runner, and is never
# handed the wrong one.
#
# One runner runs one job and is thrown away, so nothing a job leaves here is
# seen by the next.
ARG RUNNER_VERSION
ARG GO_VERSION
ARG NODE_VERSION

# Where Go and Node come from: the publishers' own images, pinned by digest,
# and only as a source to copy from - nothing of them runs here.
#
# The digest decides and the tag is for the reader, as in
# .github/runner-image/Dockerfile. Here that has a sharper edge, because the
# tag comes from versions.env and the digest is written beside it: move the
# version and leave the digest, and docker builds the old toolchain under the
# new name without a word. So the build below asks each one what version it
# is, and fails when that is not the version pinned.
#
# UPGRADING: each digest moves with its version, in the same commit:
#
#   TOKEN=$(curl -fsS "https://auth.docker.io/token?service=registry.docker.io&scope=repository:library/golang:pull" | jq -r .token)
#   curl -fsSI -H "Authorization: Bearer $TOKEN" \
#     -H "Accept: application/vnd.oci.image.index.v1+json" \
#     "https://registry-1.docker.io/v2/library/golang/manifests/<tag>" | grep -i docker-content-digest
FROM golang:${GO_VERSION}-bookworm@sha256:e8c859f5632dcfde7b32d2012b4351728f6437930887c2f6a91ea242459e5514 AS go
FROM node:${NODE_VERSION}-bookworm-slim@sha256:a9f5f7c91a432850b2a8a7797adf5eadb6c733ceed61167806cee7ea7fbc29df AS node

# The same runner, at the same digest, as the converge's image: see the note
# on this line there for how it is resolved.
FROM ghcr.io/actions/actions-runner:${RUNNER_VERSION}@sha256:e5496277be5d09bc968b3d64911b74e219ac4a3f2edce956a3ecf9271bea1ef4

ARG GO_VERSION
ARG NODE_VERSION

# Numeric, here and at the bottom, for the reason given in
# .github/runner-image/Dockerfile: a kubelet asked for runAsNonRoot cannot
# resolve a name.
USER 0

# A pipe's failure is the build's failure.
SHELL ["/bin/bash", "-o", "pipefail", "-c"]

# What the installs below and the jobs themselves need of the distribution:
# git for the checkout and for every guard that asks what is tracked; python3
# and its venv for the locked Python tools; gettext-base for envsubst, which
# fills in Flux's substitutions before validation; the rest to fetch and
# unpack.
# No compiler: nothing here builds with cgo.
#
# Unpinned for the reason .github/runner-image/Dockerfile gives - the archive
# keeps only the current version of a package - and on a base that cannot
# move.
# proved by TestTheConfigurationRulesHold (policy/conftest/rules/image_bases/image_bases.rego)
# hadolint ignore=DL3008
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates curl gettext-base git gnupg jq python3 python3-venv unzip xz-utils \
    && rm -rf /var/lib/apt/lists/*

# The runner's tool cache, where setup-go and setup-node look for a version
# before they download one. A version is found there by its directory and the
# marker beside it, which is what the two RUNs below make.
ENV RUNNER_TOOL_CACHE=/opt/hostedtoolcache

COPY --from=go --chown=1001:1001 /usr/local/go /opt/hostedtoolcache/go/${GO_VERSION}/x64
COPY --from=node --chown=1001:1001 /usr/local/bin /opt/hostedtoolcache/node/${NODE_VERSION}/x64/bin
COPY --from=node --chown=1001:1001 /usr/local/lib/node_modules /opt/hostedtoolcache/node/${NODE_VERSION}/x64/lib/node_modules

# Each is asked what it is before it is marked as that version: this is where
# a digest left behind by its version stops the build.
RUN "/opt/hostedtoolcache/go/${GO_VERSION}/x64/bin/go" version | grep -F "go${GO_VERSION} " \
    && "/opt/hostedtoolcache/node/${NODE_VERSION}/x64/bin/node" --version | grep -Fx "v${NODE_VERSION}" \
    && touch "/opt/hostedtoolcache/go/${GO_VERSION}/x64.complete" "/opt/hostedtoolcache/node/${NODE_VERSION}/x64.complete" \
    && chown 1001:1001 /opt/hostedtoolcache \
        /opt/hostedtoolcache/go "/opt/hostedtoolcache/go/${GO_VERSION}" "/opt/hostedtoolcache/go/${GO_VERSION}/x64.complete" \
        /opt/hostedtoolcache/node "/opt/hostedtoolcache/node/${NODE_VERSION}" "/opt/hostedtoolcache/node/${NODE_VERSION}/x64.complete"

# The same uid the pools run as (clusters/core/infrastructure/configs
# /pull-requests-*.yaml), asserted for the reason given in
# .github/runner-image/Dockerfile: upstream is free to renumber its account.
RUN echo "the runner account is uid $(id -u runner); the pools expect 1001" \
    && test "$(id -u runner)" = "1001"

USER 1001

# On PATH for a step that uses a tool without first asking a setup action for
# it. ~/.local/bin is where take-delivery.sh puts what it installs.
ENV PATH=/home/runner/.local/bin:/opt/hostedtoolcache/go/${GO_VERSION}/x64/bin:/opt/hostedtoolcache/node/${NODE_VERSION}/x64/bin:${PATH}

# The locked tools, by the one script allowed to install them and from the
# lock every other machine installs from - so each arrived by its hash, here
# as anywhere. As the runner's own account, because that is whose home a job
# looks in.
#
# The script and its lock are mounted for this step and are in no layer: the
# order for this image (scripts/work-orders.json) names them as what the build
# reads, the fabricator hands them over as the context called reads, and a
# change to either is a new image.
#
# Every delivery a pull request's job takes is here, and nothing else;
# TestThePullRequestRunnerCarriesExactlyWhatItsJobsTake holds this list to
# the workflows. kubectl is among them for the cluster the plan tries a
# change in, which it starts itself and which holds nothing.
RUN --mount=type=bind,from=reads,target=/reads \
    bash /reads/scripts/take-delivery.sh \
        ansible ansible-core checkov codespell etcd hadolint kube-apiserver kube-scheduler kubeconform \
        kubectl kubernetes kustomize kwokctl opentofu pre-commit pre-commit-hooks shellcheck zizmor

# Each one runs, as the account a job runs as, before the image is published.
RUN for b in go node git envsubst tofu checkov codespell conftest etcd hadolint kube-apiserver kube-scheduler \
        kubeconform kubectl kustomize kwokctl opa pre-commit regal semgrep shellcheck zizmor; do \
        command -v "$b" >/dev/null || { echo "missing: $b"; exit 1; }; \
    done
