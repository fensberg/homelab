#!/usr/bin/env bash
# Installs tools from scripts/deliveries.lock, refusing any file whose SHA256
# the lock does not list.
#
#     scripts/take-delivery.sh checkov                      # installs checkov
#     scripts/take-delivery.sh pre-commit pre-commit-hooks  # installs both
#     scripts/take-delivery.sh hadolint                     # installs hadolint
#     scripts/take-delivery.sh --print checkov              # prints its lock lines, installs nothing
#
# This is the only place in the repository that runs `pip install` or installs
# a tool the lock covers, and tests/go/repo/deliveries_test.go refuses any
# other. That is the point: a version pin written anywhere else fixes the tool
# and lets everything it depends on float, and a binary downloaded anywhere
# else is checked against whatever that one place remembered to check (#416).
#
# Where things land, for every caller alike, and never anywhere needing root -
# the CI lanes run with sudo removed (#410):
#
#   pypi   one isolated virtual environment, ~/.local/share/deliveries/pypi,
#          with each named tool's own commands linked into ~/.local/bin and
#          the environment's Python there as `delivered-python`
#   fetch  ~/.local/bin/<name>; an archive gives up the one file named <name>
#
# WHY AN ISOLATED ENVIRONMENT (#423). The first version installed into the
# user site with --break-system-packages, and the first real workstation run
# showed what that costs. pip's --require-hashes checks what it downloads, not
# what it finds installed, so a package the OS already ships - jinja2, from
# Debian - was accepted with no hash compared at all. And the user site comes
# before the OS's own packages on sys.path, so the locked packaging 23.2
# replaced Debian's 25.0 for every Python program that account ran, the OS's
# own included. An environment created without system site packages sees
# neither: every package in it arrived by hash, and nothing in it is visible to
# anything else.
#
# `delivered-python` is a wrapper rather than a link: a symlink to a virtual
# environment's Python starts the system interpreter without the environment.
#
# Under GitHub Actions, ~/.local/bin is added to GITHUB_PATH, so later steps
# find what was delivered by name.
#
# Several Python tools at once are one pip run: each tool's section is its
# closure within ONE resolution of every tool together, so lines shared between
# sections are identical and are de-duplicated before pip sees them.
set -euo pipefail

lock="$(cd "$(dirname "$0")" && pwd)/deliveries.lock"
bin="${HOME}/.local/bin"

usage() {
	echo "usage: $0 [--print] <name>..." >&2
	echo "installs each named delivery from scripts/deliveries.lock, by hash" >&2
	exit 2
}

print=false
if [ "${1:-}" = "--print" ]; then
	print=true
	shift
fi
[ "$#" -gt 0 ] || usage

# header <name> prints the one lock header naming it: "# [kind: name KEY=version]".
header() {
	awk -v name="$1" '
		/^# \[[a-z]+: / {
			split($0, f, " ")
			if (f[3] == name) print
		}' "$lock"
}

# section <header> prints the lines between that header and its # [end].
section() {
	awk -v h="$1" '$0 == h { on = 1; next } $0 == "# [end]" { on = 0 } on' "$lock"
}

pypi=""
fetches=()
for name in "$@"; do
	# The name is matched literally against a header, but checking its shape
	# first means a typo reads as a typo rather than as a missing delivery.
	if ! [[ $name =~ ^[a-z0-9][a-z0-9-]*$ ]]; then
		echo "'$name' is not a delivery name as the lock writes them." >&2
		exit 2
	fi
	h="$(header "$name")"
	if [ -z "$h" ]; then
		echo "$name is not in scripts/deliveries.lock. Declare how it is delivered (pypi: or fetch:) on its tools: entry in scripts/approved-suppliers.yml, pin its version in scripts/versions.env, and run: task order-deliveries" >&2
		exit 1
	fi
	if [ "$(printf '%s\n' "$h" | wc -l)" -ne 1 ]; then
		echo "$name has more than one section in scripts/deliveries.lock, so which to install is not decided. security guard-deliveries refuses this lock." >&2
		exit 1
	fi
	lines="$(section "$h")"
	if [ -z "$lines" ]; then
		echo "$name's section in scripts/deliveries.lock is empty." >&2
		exit 1
	fi
	if [ "$print" = true ]; then
		printf '%s\n' "$lines"
		continue
	fi
	case "$h" in
	"# [pypi: "*) pypi="${pypi}${lines}"$'\n' ;;
	"# [fetch: "*) fetches+=("$name") ;;
	*)
		echo "$name is a kind of delivery this script does not know how to take: $h" >&2
		exit 1
		;;
	esac
done
[ "$print" = false ] || exit 0

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$bin"

if [ -n "$pypi" ]; then
	venv="${HOME}/.local/share/deliveries/pypi"
	# Rebuilt when its interpreter no longer runs - an OS upgrade that moves
	# python3 leaves an environment pointing at a Python that has gone.
	if ! "$venv/bin/python" -c '' 2>/dev/null; then
		rm -rf "$venv"
		python3 -m venv "$venv"
	fi
	printf '%s' "$pypi" | sort -u >"$work/requirements.txt"
	"$venv/bin/python" -m pip install --require-hashes -r "$work/requirements.txt"

	printf '%s\n' \
		'#!/bin/sh' \
		'# Written by scripts/take-delivery.sh: the Python of the environment every' \
		'# locked Python tool is installed into. Hooks run their checks with it.' \
		"exec \"$venv/bin/python\" \"\$@\"" >"$bin/delivered-python"
	chmod 0755 "$bin/delivered-python"

	# Each named tool's own commands, and nothing its dependencies bring: those
	# are the tool's business, and linking them would put a hundred commands
	# on PATH that nobody asked for. Read from the files the package itself
	# installed into bin/, because a command is not always an entry point -
	# checkov ships a plain script and zizmor a compiled binary.
	for name in "$@"; do
		case "$(header "$name")" in "# [pypi: "*) ;; *) continue ;; esac
		commands="$("$venv/bin/python" -c 'import sys, os
from importlib.metadata import distribution
d = distribution(sys.argv[1])
bindir = os.path.realpath(os.path.join(sys.prefix, "bin"))
for f in d.files or []:
    p = os.path.realpath(d.locate_file(f))
    if os.path.dirname(p) == bindir and not p.endswith(".cmd"):
        print(os.path.basename(p))' "$name")"
		if [ -z "$commands" ]; then
			echo "refusing $name: it installed no command, so there is nothing to put on PATH - is it a library rather than a tool?" >&2
			exit 1
		fi
		for command in $commands; do
			ln -sf "$venv/bin/$command" "$bin/$command"
		done
	done
fi

for name in "${fetches[@]}"; do
	h="$(header "$name")"
	read -r url hash <<<"$(section "$h")"
	sha="${hash#--hash=sha256:}"
	version="${h##*=}"
	version="${version%]}"
	file="$work/${url##*/}"

	curl --proto '=https' --tlsv1.2 -fsSL -o "$file" "$url"
	# The whole point. A file that is not the one ordered is refused before
	# anything reads it, let alone runs it.
	if ! echo "${sha}  ${file}" | sha256sum -c --quiet -; then
		echo "refusing $name: ${url} is not the file scripts/deliveries.lock was ordered with. If the publisher changed it on purpose, order again: task order-deliveries" >&2
		exit 1
	fi

	case "$file" in
	*.tar.gz | *.tgz | *.tar.xz)
		mkdir "$work/$name.d"
		tar -xf "$file" -C "$work/$name.d"
		found="$(find "$work/$name.d" -type f -name "$name")"
		if [ -z "$found" ] || [ "$(printf '%s\n' "$found" | wc -l)" -ne 1 ]; then
			echo "refusing $name: its archive does not hold exactly one file called $name" >&2
			exit 1
		fi
		install -m 0755 "$found" "$bin/$name"
		;;
	*.sh)
		# A publisher's installer, run to install the version the lock header
		# carries. Each needs its own flags, so each is named here, and one this
		# does not know is refused rather than run with a guess.
		case "$name" in
		opentofu)
			# Into directories this account owns; the installer's defaults fall
			# back to sudo.
			sh "$file" --install-method standalone --opentofu-version "$version" \
				--install-path "${HOME}/.local/opentofu" --symlink-path "$bin"
			;;
		*)
			echo "refusing $name: it is an installer script and nothing here says how to run it" >&2
			exit 1
			;;
		esac
		;;
	*)
		install -m 0755 "$file" "$bin/$name"
		;;
	esac
	echo "took delivery of $name $version"
done

if [ -n "${GITHUB_PATH:-}" ]; then
	echo "$bin" >>"$GITHUB_PATH"
fi
