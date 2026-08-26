#!/usr/bin/env bash
# One-time workstation setup for the homelab project. Run once, on a fresh
# Linux devbox. Installs every tool the ignition CLI (scripts/ignite) needs.
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
GO_VERSION=1.27.0
if has go; then
	skip "go already present ($(go version))"
else
	info "downloading go${GO_VERSION}.linux-${GOARCH}.tar.gz"
	curl -fsSL -o "$TMP/go.tar.gz" "https://go.dev/dl/go${GO_VERSION}.linux-${GOARCH}.tar.gz"
	sudo rm -rf /usr/local/go
	sudo tar -C /usr/local -xzf "$TMP/go.tar.gz"
	grep -q '/usr/local/go/bin' ~/.bashrc 2>/dev/null ||
		echo 'export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin' >>~/.bashrc
	export PATH="$PATH:/usr/local/go/bin"
	ok "go ${GO_VERSION} installed"
fi

step "OpenTofu (pinned)"
TOFU_VERSION=1.12.6
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
TALOSCTL_VERSION=v1.11.2
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
FLUX_VERSION=2.7.1
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

step "rclone"
if has rclone; then
	skip "rclone already present ($(rclone version | head -1))"
else
	info "installing rclone via the official install script"
	curl -fsSL https://rclone.org/install.sh | sudo bash
	ok "rclone installed"
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

echo
ok "all dependencies present. Run 'task start' to ignite."
