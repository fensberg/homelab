# The image CI runs steward in.
#
# The stock actions-runner image is deliberately minimal - no op, no tofu, no
# age, no rclone - so `steward plan` failed at Render with "1Password CLI not
# found on PATH". This layers exactly the binaries steward shells out to, and
# nothing else.
#
# Versions are build arguments fed from scripts/versions.env, the same file
# scripts/install-dependencies.sh reads to provision a workstation. They are
# not restated here: a plan produced by one version of tofu and applied by
# another is a difference nobody sees until it matters, and a self-hosted
# runner is exactly where that drift hides.
ARG RUNNER_VERSION=2.331.0
FROM ghcr.io/actions/actions-runner:${RUNNER_VERSION}

ARG TOFU_VERSION
ARG RCLONE_VERSION
ARG TARGETARCH=amd64

USER root

# ca-certificates and curl to fetch the rest; gnupg for 1Password's repository
# key; jq because the workflows use it. age comes from Debian, like the
# workstation script installs it.
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates curl gnupg jq unzip age \
    && rm -rf /var/lib/apt/lists/*

# 1Password CLI, from 1Password's own repository - the same source
# install-dependencies.sh uses. Pinned by the repository's own release stream
# rather than a version argument, because that is how the vendor ships it.
RUN install -d -m 0755 /usr/share/keyrings \
    && curl -fsSL https://downloads.1password.com/linux/keys/1password.asc \
        | gpg --dearmor -o /usr/share/keyrings/1password-archive-keyring.gpg \
    && echo "deb [arch=${TARGETARCH} signed-by=/usr/share/keyrings/1password-archive-keyring.gpg] https://downloads.1password.com/linux/debian/${TARGETARCH} stable main" \
        > /etc/apt/sources.list.d/1password.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends 1password-cli \
    && rm -rf /var/lib/apt/lists/*

# OpenTofu, from the release archive rather than the official install script.
# Same reasoning as install-dependencies.sh: that script is unpinned and
# unverified and gets piped into a shell.
RUN curl -fsSL -o /tmp/tofu.zip \
        "https://github.com/opentofu/opentofu/releases/download/v${TOFU_VERSION}/tofu_${TOFU_VERSION}_linux_${TARGETARCH}.zip" \
    && unzip -q /tmp/tofu.zip tofu -d /usr/local/bin \
    && chmod +x /usr/local/bin/tofu \
    && rm /tmp/tofu.zip \
    && tofu version

# rclone, from the pinned .deb - the same artefact the workstation installs,
# and the version whose absence made every backup fail its first attempt with
# a 501 until the pin was actually enforced.
RUN curl -fsSL -o /tmp/rclone.deb \
        "https://github.com/rclone/rclone/releases/download/v${RCLONE_VERSION}/rclone-v${RCLONE_VERSION}-linux-${TARGETARCH}.deb" \
    && apt-get update \
    && apt-get install -y --no-install-recommends /tmp/rclone.deb \
    && rm -rf /tmp/rclone.deb /var/lib/apt/lists/* \
    && rclone version | head -1

# kubectl, for the Health phase. Pinned to the Talos-supported minor rather
# than "stable", so an upstream release cannot change what CI runs overnight.
ARG KUBECTL_VERSION=v1.34.11
RUN curl -fsSL -o /usr/local/bin/kubectl \
        "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${TARGETARCH}/kubectl" \
    && chmod +x /usr/local/bin/kubectl \
    && kubectl version --client=true --output=yaml | head -3

# Prove the toolchain is complete before the image is ever published. A missing
# binary here surfaces as a job that fails at Render, on the runner, minutes
# into a converge - which is exactly how this image came to exist.
RUN for b in op tofu age rclone kubectl jq; do \
        command -v "$b" >/dev/null || { echo "missing: $b"; exit 1; }; \
    done

USER runner
