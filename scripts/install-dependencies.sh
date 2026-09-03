#!/usr/bin/env bash
# One-time workstation setup for the homelab project. Run once, on a fresh
# Linux devbox. Installs every tool the ignition CLI (scripts/contractor) needs.
#
# Deliberately plain bash, not Go: this script's job includes installing Go
# itself, so it cannot depend on Go already being present to run.
#
# Safe to re-run. Anything already on PATH is left alone - this checks for
# presence, not for a specific pinned version, same as the tool it replaces.
set -euo pipefail

info() { printf '  -> %s\n' "$1"; }
ok() { printf '  \033[32m[ok]\033[0m %s\n' "$1"; }
skip() { printf '  \033[90m[skip]\033[0m %s\n' "$1"; }
warn() { printf '  \033[33m[warn]\033[0m %s\n' "$1"; }
fail() { printf '  \033[31m[FAIL]\033[0m %s\n' "$1"; }
step() { printf '\n\033[36m=== %s\033[0m\n' "$1"; }

has() { command -v "$1" >/dev/null 2>&1; }

# Tool versions live in one file, shared with the runner image's Dockerfile.
# See scripts/versions.env for why they are not restated in either place.
# shellcheck source=scripts/versions.env
# shellcheck disable=SC1091  # ShellCheck needs --external-sources to follow a
# sourced file, and CI does not pass it. The source= directive above says where
# the file is for anyone reading; this silences the note about not following it.
. "$(dirname "$0")/versions.env"

ARCH="$(uname -m)"
case "$ARCH" in
x86_64) GOARCH=amd64 ;;
aarch64) GOARCH=arm64 ;;
*)
	fail "Unsupported architecture: $ARCH"
	exit 1
	;;
esac

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

need_apt=()

step "Base packages (git, jq, curl, unzip)"
for pkg in git jq curl unzip; do
	if has "$pkg"; then
		skip "$pkg already present"
	else
		need_apt+=("$pkg")
	fi
done
if [ "${#need_apt[@]}" -gt 0 ]; then
	info "installing: ${need_apt[*]}"
	sudo apt-get update -qq
	sudo apt-get install -y "${need_apt[@]}"
	ok "base packages installed"
fi

step "Go (pinned)"
if has go; then
	skip "go already present ($(go version))"
else
	info "downloading go${GO_VERSION}.linux-${GOARCH}.tar.gz"
	curl -fsSL -o "$TMP/go.tar.gz" "https://go.dev/dl/go${GO_VERSION}.linux-${GOARCH}.tar.gz"
	sudo rm -rf /usr/local/go
	sudo tar -C /usr/local -xzf "$TMP/go.tar.gz"
	# shellcheck disable=SC2016 # single-quoted on purpose: $PATH/$HOME must
	# expand when .bashrc is later sourced, not now.
	grep -q '/usr/local/go/bin' ~/.bashrc 2>/dev/null ||
		echo 'export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin' >>~/.bashrc
	export PATH="$PATH:/usr/local/go/bin"
	ok "go ${GO_VERSION} installed"
fi

step "Node.js (pinned)"
# Needed only by the JavaScript/TypeScript test tier (vitest, playwright,
# tsc - see tests/README.md). Nothing in the ignition path touches Node, so a
# failure here does not stop the button working; `task test:js` self-skips
# when it is absent.
#
# Installed from the official tarball rather than apt for the same reason Go
# is: Debian ships whatever it ships, and this project pins. Both checksums
# are recorded here rather than fetched from SHASUMS256.txt at run time -
# fetching the checksum from the same place as the artefact proves only that
# the download was not corrupted in transit, not that it is the file this
# repository was tested against.
case "$GOARCH" in
amd64) NODE_ARCH=x64 NODE_SHA256=14b342e71204f811bde6153be8e04b62aef63c236fef92b55f9c83154b409647 ;;
arm64) NODE_ARCH=arm64 NODE_SHA256=01443c1e1a29e531ccad5a46fefa6df490d2189c49f7955904aecdbb0fe86fdc ;;
esac
if has node; then
	skip "node already present ($(node --version))"
else
	info "downloading node-v${NODE_VERSION}-linux-${NODE_ARCH}.tar.xz"
	curl -fsSL -o "$TMP/node.tar.xz" \
		"https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-${NODE_ARCH}.tar.xz"
	echo "${NODE_SHA256}  $TMP/node.tar.xz" | sha256sum -c -
	sudo rm -rf /usr/local/node
	sudo mkdir -p /usr/local/node
	sudo tar -C /usr/local/node --strip-components=1 -xJf "$TMP/node.tar.xz"
	for bin in node npm npx corepack; do
		sudo ln -sf "/usr/local/node/bin/${bin}" "/usr/local/bin/${bin}"
	done
	ok "node ${NODE_VERSION} installed"
fi

step "pnpm (pinned by package.json)"
# corepack reads the "packageManager" field in package.json and installs
# exactly that version, so the pin lives with the project rather than in this
# script - one place to change it, and CI reads the same field.
if has pnpm; then
	skip "pnpm already present ($(pnpm --version))"
elif has corepack; then
	info "activating pnpm via corepack"
	sudo COREPACK_ENABLE_DOWNLOAD_PROMPT=0 corepack enable --install-directory /usr/local/bin
	ok "pnpm activated"
else
	warn "corepack not found - the JavaScript test tier will be skipped"
fi

step "OpenTofu (pinned)"
if has tofu; then
	skip "tofu already present ($(tofu version | head -1))"
else
	info "installing tofu ${TOFU_VERSION} via the official install script"
	curl -fsSL https://get.opentofu.org/install-opentofu.sh -o "$TMP/install-opentofu.sh"
	chmod +x "$TMP/install-opentofu.sh"
	"$TMP/install-opentofu.sh" --install-method standalone --opentofu-version "$TOFU_VERSION"
	ok "tofu ${TOFU_VERSION} installed"
fi

step "1Password CLI"
if has op; then
	skip "op already present ($(op --version))"
else
	info "installing op via 1Password's apt repo"
	curl -fsSL https://downloads.1password.com/linux/keys/1password.asc |
		sudo gpg --dearmor --output /usr/share/keyrings/1password-archive-keyring.gpg
	echo "deb [arch=${GOARCH} signed-by=/usr/share/keyrings/1password-archive-keyring.gpg] https://downloads.1password.com/linux/debian/${GOARCH} stable main" |
		sudo tee /etc/apt/sources.list.d/1password.list >/dev/null
	sudo apt-get update -qq
	sudo apt-get install -y 1password-cli
	ok "op installed"
fi

step "kubectl"
if has kubectl; then
	skip "kubectl already present ($(kubectl version --client -o yaml 2>/dev/null | grep gitVersion | head -1))"
else
	KUBECTL_VERSION="$(curl -fsSL https://dl.k8s.io/release/stable.txt)"
	info "installing kubectl ${KUBECTL_VERSION}"
	curl -fsSL -o "$TMP/kubectl" "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${GOARCH}/kubectl"
	chmod +x "$TMP/kubectl"
	sudo mv "$TMP/kubectl" /usr/local/bin/kubectl
	ok "kubectl ${KUBECTL_VERSION} installed"
fi

step "talosctl (pinned)"
if has talosctl; then
	skip "talosctl already present"
else
	info "installing talosctl ${TALOSCTL_VERSION}"
	curl -fsSL -o /usr/local/bin/talosctl \
		"https://github.com/siderolabs/talos/releases/download/${TALOSCTL_VERSION}/talosctl-linux-${GOARCH}"
	sudo chmod +x /usr/local/bin/talosctl
	ok "talosctl ${TALOSCTL_VERSION} installed"
fi

step "flux (pinned)"
if has flux; then
	skip "flux already present"
else
	info "installing flux ${FLUX_VERSION}"
	curl -fsSL "https://github.com/fluxcd/flux2/releases/download/v${FLUX_VERSION}/flux_${FLUX_VERSION}_linux_${GOARCH}.tar.gz" \
		-o "$TMP/flux.tar.gz"
	tar -C "$TMP" -xzf "$TMP/flux.tar.gz" flux
	sudo mv "$TMP/flux" /usr/local/bin/flux
	ok "flux ${FLUX_VERSION} installed"
fi

step "age"
if has age; then
	skip "age already present"
else
	info "installing age via apt"
	sudo apt-get install -y age
	ok "age installed"
fi

step "rclone (pinned)"
# Presence is not the same question as version, and this step is the one place
# that difference has teeth. Debian ships an rclone package that arrives as a
# dependency of other things, so the binary can already be on PATH years older
# than the pin - and `has rclone` would then skip, print whatever it found, and
# leave the step called "pinned" enforcing nothing. R2 is the backend least
# tolerant of that: an old client fails S3 operations R2 answers with 501
# NotImplemented, and rclone's own retry can turn that into a success nobody
# reads, which is how a broken backup passes for a working one.
rclone_installed_version() {
	rclone version 2>/dev/null | awk 'NR == 1 { sub(/^v/, "", $2); print $2 }'
}
if has rclone && [ "$(rclone_installed_version)" = "$RCLONE_VERSION" ]; then
	skip "rclone already at the pinned ${RCLONE_VERSION}"
else
	if has rclone; then
		# Includes distribution rebuilds, which report a -DEV suffix and so
		# never compare equal to the pin. That is deliberate: the pin names a
		# release binary, and a rebuild of the same version number is not it.
		warn "rclone $(rclone_installed_version) is not the pinned ${RCLONE_VERSION} - replacing it"
	fi
	# Not the official install script: it pipes an unpinned, unverified
	# remote script straight into `sudo bash`, which is both a real
	# supply-chain risk and unpinned, contrary to this project's own rule.
	# A pinned .deb from the same GitHub release everything else here
	# downloads from is no less official and never executes anything the
	# way a fetched shell script would.
	info "installing rclone ${RCLONE_VERSION}"
	curl -fsSL -o "$TMP/rclone.deb" \
		"https://github.com/rclone/rclone/releases/download/v${RCLONE_VERSION}/rclone-v${RCLONE_VERSION}-linux-${GOARCH}.deb"
	sudo apt-get install -y "$TMP/rclone.deb"
	ok "rclone ${RCLONE_VERSION} installed"
fi

step "task (go-task)"
if has task; then
	skip "task already present ($(task --version))"
else
	info "installing task via the official install script"
	curl -fsSL https://taskfile.dev/install.sh | sudo sh -s -- -d -b /usr/local/bin
	ok "task installed"
fi

step "gh (GitHub CLI)"
if has gh; then
	skip "gh already present ($(gh --version | head -1))"
else
	info "installing gh via GitHub's apt repo"
	curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg |
		sudo dd of=/usr/share/keyrings/githubcli-archive-keyring.gpg
	echo "deb [arch=${GOARCH} signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" |
		sudo tee /etc/apt/sources.list.d/github-cli.list >/dev/null
	sudo apt-get update -qq
	sudo apt-get install -y gh
	ok "gh installed"
fi

step "Ansible"
if has ansible-playbook; then
	skip "ansible already present ($(ansible --version | head -1))"
else
	info "installing ansible-core via pip"
	python3 -m pip install --user --break-system-packages ansible-core
	ok "ansible installed"
fi

step "Ansible collections"
# ansible-core does not bundle community collections the way the full
# 'ansible' metapackage does, so hypervisor-prep.yml's use of
# community.crypto and ansible.posix needs them installed explicitly. Pinned
# in requirements.yml alongside the playbook; install is idempotent, so this
# runs unconditionally rather than trying to detect what is already present.
ansible-galaxy collection install -r "$(dirname "$0")/../management/hypervisor/requirements.yml"
ok "ansible collections installed"

step "pre-commit"
if has pre-commit; then
	skip "pre-commit already present ($(pre-commit --version))"
else
	info "installing pre-commit via pip"
	python3 -m pip install --user --break-system-packages pre-commit
	ok "pre-commit installed"
fi
# Installing the tool is not enough on its own: without this, pre-commit only
# ever runs when invoked by hand (`task fix`), never automatically on `git
# commit` - which is exactly how an unformatted file landed in a real commit
# on this checkout. This writes .git/hooks/pre-commit so a commit cannot skip
# it by simply forgetting to run `task fix` first.
# Point git at the versioned shims rather than at pre-commit's own generated
# hook, and the reason is the ordering of one thing.
#
# pre-commit clones a hook repository and installs its environment BEFORE it
# runs any hook, and installing runs setup code - npm install, pip install, a
# Go build. So a supplier guard that is itself a pre-commit hook runs after the
# code it exists to refuse has already executed on this machine.
#
# githooks/pre-commit is what git invokes. Nothing third-party has run at that
# point. It runs the guard, and only reaches pre-commit if the guard passes.
#
# Versioned rather than written into .git/hooks: a hook that lives only there
# is a hook nobody can review, and `pre-commit install` would overwrite it.
info "wiring the git hooks"
(
	cd "$(dirname "$0")/.."
	git config core.hooksPath githooks

	# Still install pre-commit's own hook environments, so the first real
	# commit is not also the first download. `install-hooks` clones and builds
	# without running anything - and it happens here, once, where somebody is
	# watching, rather than inside a commit.
	pre-commit install-hooks
)
ok "git hooks wired to githooks/, with the supplier guard ahead of pre-commit"

# ---------------------------------------------------------------------------
# Commit signing
# ---------------------------------------------------------------------------
#
# The pre-push hook refuses to publish unsigned commits. That is deliberate,
# but it is only reasonable if signing actually works here, and it did not:
# the agent publishes through the GitHub App, which has no user account and so
# no signing key, while a human has an account and can hold one. The human was
# told the three commands in a chat message and blocked twice by the hook in
# the meantime, which is the wrong way round - a step a computer can do is not
# a step to ask somebody for.
#
# Configured for this repository only. Nothing here should quietly change how
# somebody's other checkouts behave.
#
# Registering the key with GitHub is NOT required for a push to succeed. The
# hook asks whether a signature exists, not whether GitHub can verify it, so
# these three lines are enough to unblock `git push` and the editor's sync
# button. Registering it only earns the "Verified" badge, which is why it is
# printed as optional rather than as a blocker.
step "commit signing"
repo_root="$(cd "$(dirname "$0")/.." && pwd)"

if git -C "$repo_root" config --get commit.gpgsign | grep -qx true; then
	skip "commit signing already configured"
else
	signing_key=""
	for candidate in ~/.ssh/id_ed25519.pub ~/.ssh/id_rsa.pub; do
		if [ -f "$candidate" ]; then
			signing_key="$candidate"
			break
		fi
	done

	if [ -z "$signing_key" ]; then
		info "no ssh key found; generating one for signing"
		ssh-keygen -q -t ed25519 -N "" -f ~/.ssh/id_ed25519
		signing_key=~/.ssh/id_ed25519.pub
	fi

	git -C "$repo_root" config gpg.format ssh
	git -C "$repo_root" config user.signingkey "$signing_key"
	git -C "$repo_root" config commit.gpgsign true
	ok "commits from this checkout are now signed with $signing_key"
	info "optional: add that key to GitHub a second time, as a Signing key"
	info "  (Settings > SSH and GPG keys > New SSH key > Key type: Signing)"
	info "  Pushes work without it; it is what earns the Verified badge."
fi

echo
ok "all dependencies present."
info "Run 'task test' to check everything still behaves, then 'task start' to ignite."
