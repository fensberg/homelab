#!/usr/bin/env bash
# The workstation's side of its own tunnel: the connector, and the lock on it.
#
#     workstation/tunnel.sh install     # as yourself, on the workstation; asks for sudo
#     workstation/tunnel.sh check       # is it connected, and does the lock hold
#     workstation/tunnel.sh remove      # take all of it off again
#     workstation/tunnel.sh rules <address> <uid> <resolver>...   # print the lock, change nothing
#
# WHAT THIS IS FOR. The workstation is reached from away through a tunnel of
# its own (management/estate/workstation.tf), not through a site's: a site's
# connector runs in that site's cluster, and the day the cluster is broken is
# the day the machine that repairs it has to be reachable. So the connector
# runs here, as a service, and depends on nothing the estate builds.
#
# WHAT MAKES IT SAFE IS HERE, NOT AT THE VENDOR. A connector forwards wherever
# the account's routes send it, and the account is somebody else's computer.
# So the connector runs as a user of its own with no shell and no home, and a
# firewall rule on this machine holds that user to three things: this
# machine's own SSH port, the name resolver, and the vendor's edge. It cannot
# open the router, the hypervisor, the overlay, or any other port here -
# whatever routes the tunnel is given, by mistake or by somebody who took the
# account. Getting in still takes an enrolled device and an SSH key.
#
# The lock is a unit the connector is bound to: if the rule cannot be loaded
# the connector does not start, and if the rule is taken away the connector
# stops. `install` and `check` then prove it from the connector's own user -
# its SSH port answers, and the gateway and this machine's other addresses do
# not.
#
# THE ADDRESS IS THE WORKSTATION'S OWN, NOT THE HOUSE'S. What the estate
# routes is one address written in config/estate.tpl.json, and the lock unit
# puts it on this machine's loopback. So the route does not move when the
# house's router hands this machine a different address, and it is in a range
# no hotel or office network hands out, so it cannot be shadowed by wherever
# the operator happens to be.
#
# THE TUNNEL'S TOKEN IS PULLED, NOT GRANTED. Nothing keeps it: the estate
# does not grant it into a vault, so no vault and no service account gained
# any reach for this. `install` asks the account for it, once, with the same
# token the estate is converged with - read from the estate's vault, used for
# two questions and dropped. It finds the tunnel by the one route the estate
# made to this machine's address, and asks for that tunnel's token. The token
# goes into a file only the connector's group can read, and is never printed.
#
# So run `install` with a 1Password session that reads the estate's vault:
# the token the lawyer runs with.
set -euo pipefail

name="workstation-tunnel"
user="$name"
table="workstation_tunnel"
etc="/etc/$name"
libexec="/usr/local/libexec/$name"
lock_unit="$name-lock.service"
unit="$name.service"
ssh_port=22

api="https://api.cloudflare.com/client/v4"

here="$(cd "$(dirname "$0")" && pwd)"
template="$here/../config/estate.tpl.json"

say() { printf '  %s\n' "$*"; }
fail() {
	printf 'workstation tunnel: %s\n' "$*" >&2
	exit 1
}

ipv4='^([0-9]{1,3}\.){3}[0-9]{1,3}$'

# rules prints the lock: what the connector's user may open, and nothing else.
rules() {
	local address="$1" uid="$2"
	shift 2
	[[ $address =~ $ipv4 ]] || fail "'$address' is not an IPv4 address, so there is no SSH port of this machine's to allow"
	[[ $uid =~ ^[0-9]+$ ]] || fail "'$uid' is not a user id, so there is nobody to hold to the lock"
	[ "$#" -gt 0 ] || fail "no resolver was given; the connector finds the vendor's edge by name and would never connect"
	local r
	for r in "$@"; do
		[[ $r =~ $ipv4 ]] || fail "the resolver '$r' is not an IPv4 address"
	done

	cat <<EOF
# Written by workstation/tunnel.sh. Loaded by $lock_unit, which the
# connector is bound to: no lock, no connector.
table inet $table
delete table inet $table

table inet $table {
	chain output {
		type filter hook output priority filter; policy accept;

		# Only the connector is held to this.
		meta skuid != $uid accept

		# What it was already allowed to open stays open. Only a new
		# connection is judged below.
		ct state established,related accept

		# The one thing in the house it may open: this machine's own SSH.
		ip daddr $address tcp dport $ssh_port accept

		# Name lookups, to the resolvers this machine uses.
EOF
	for r in "$@"; do
		printf '\t\tip daddr %s udp dport 53 accept\n' "$r"
		printf '\t\tip daddr %s tcp dport 53 accept\n' "$r"
	done
	cat <<EOF

		# Nothing else that is private: not the house, not the overlay, not
		# any other port or address of this machine.
		ip daddr { 0.0.0.0/8, 10.0.0.0/8, 100.64.0.0/10, 127.0.0.0/8, 169.254.0.0/16, 172.16.0.0/12, 192.168.0.0/16, 198.18.0.0/15, 224.0.0.0/3 } reject
		ip6 daddr { ::1, fc00::/7, fe80::/10, ff00::/8 } reject

		# The vendor's edge, and nothing else out there.
		meta l4proto { tcp, udp } th dport 7844 accept
		tcp dport 443 accept
		reject
	}
}
EOF
}

lock_unit_text() {
	cat <<EOF
[Unit]
Description=Hold the workstation's tunnel connector to this machine's SSH port
Before=$unit
After=network-pre.target

[Service]
Type=oneshot
RemainAfterExit=yes
# The address the estate routes here, on loopback: this machine's own
# whatever the house's network calls it.
ExecStart=/usr/sbin/ip address replace $1/32 dev lo
ExecStart=/usr/sbin/nft -f $etc/lock.nft
ExecStop=/usr/sbin/nft delete table inet $table
ExecStop=/usr/sbin/ip address del $1/32 dev lo

[Install]
WantedBy=multi-user.target
EOF
}

unit_text() {
	cat <<EOF
[Unit]
Description=The workstation's own tunnel connector
Wants=network-online.target
After=network-online.target $lock_unit
# No lock, no connector: started only once the rule is loaded, and stopped
# if the rule is taken away.
BindsTo=$lock_unit

[Service]
User=$user
ExecStart=$libexec/cloudflared tunnel --no-autoupdate run --token-file $etc/token
Restart=always
RestartSec=5
NoNewPrivileges=yes
CapabilityBoundingSet=
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
PrivateDevices=yes
RestrictSUIDSGID=yes

[Install]
WantedBy=multi-user.target
EOF
}

# resolvers is the IPv4 name servers this machine asks.
resolvers() {
	awk '$1 == "nameserver" && $2 ~ /^[0-9.]+$/ { print $2 }' /etc/resolv.conf
}

# opens reports whether the connector's user can open a TCP port.
opens() {
	sudo -u "$user" timeout 4 bash -c "exec 3<>/dev/tcp/$1/$2" 2>/dev/null
}

# gateway is where this machine sends what is not local: the nearest thing in
# the house that is not this machine.
gateway() {
	ip -4 route show default | awk '{ print $3; exit }'
}

# connected reports whether the connector has registered with the vendor's
# edge since this machine started.
#
# The log is read whole and then searched. Piped into `grep -q`, grep stops
# at the first match, the log reader is killed writing the rest, and under
# pipefail a connector that had connected four times read as not connected.
connected() {
	local log
	log="$(sudo journalctl -u "$unit" -b --no-pager -n 400 2>/dev/null)" || return 1
	grep -q "Registered tunnel connection" <<<"$log"
}

# check proves the lock from the connector's own user, and that the connector
# is connected.
check() {
	local address failed=0
	# Read as root: the lock lives beside the connector's token, in a
	# directory only root and the connector's group can open.
	address="$(sudo sed -n 's/^[[:space:]]*ip daddr \([0-9.]*\) tcp dport '"$ssh_port"' accept$/\1/p' "$etc/lock.nft" | head -n 1)"
	[ -n "$address" ] || fail "$etc/lock.nft names no address, so the tunnel is not installed here. Run: workstation/tunnel.sh install"

	systemctl is-active --quiet "$lock_unit" || fail "the lock is not loaded ($lock_unit is not active), so the connector should not be running either"

	if opens "$address" "$ssh_port"; then
		say "[ok]   the connector can open this machine's SSH port"
	else
		say "[FAIL] the connector cannot open this machine's SSH port, so nothing arriving through the tunnel reaches it"
		failed=1
	fi

	# What it must not open, each tried first as you: a refusal only proves
	# the lock where the same port answers somebody who is not held to it.
	local gw target port answered=0
	gw="$(gateway)"
	for target in "127.0.0.1:$ssh_port" "$gw:80" "$gw:443" "$gw:53"; do
		port="${target##*:}"
		target="${target%:*}"
		[ -n "$target" ] || continue
		timeout 4 bash -c "exec 3<>/dev/tcp/$target/$port" 2>/dev/null || continue
		answered=$((answered + 1))
		if opens "$target" "$port"; then
			say "[FAIL] the connector opened a port it must not: one of this house's, or another of this machine's"
			failed=1
		fi
	done
	if [ "$answered" -eq 0 ]; then
		say "[FAIL] nothing tried here answers even for you, so the lock was proved against nothing"
		failed=1
	elif [ "$failed" -eq 0 ]; then
		say "[ok]   the connector cannot open $answered port(s) that answer for you: the gateway's, and this machine's own on another address"
	fi

	if ! systemctl is-active --quiet "$unit"; then
		say "[FAIL] the connector is not running ($unit)"
		failed=1
	elif connected; then
		say "[ok]   the connector is connected to the vendor's edge"
	else
		say "[FAIL] the connector is running and has not connected. See: sudo journalctl -u $unit -n 50"
		failed=1
	fi
	return "$failed"
}

# vault_or_value is a field of the estate's template: read from the vault
# when it is a reference to one, and as it is written otherwise.
vault_or_value() {
	local value
	value="$(jq -r "$1 // empty" "$template")" || fail "could not read $template"
	if [[ $value =~ ^\{\{[[:space:]]*(op://[^[:space:]]+)[[:space:]]*\}\}$ ]]; then
		value="$(op read "${BASH_REMATCH[1]}")" || fail "could not read $1 from the vault. Sign in with the estate's token"
	fi
	printf '%s' "$value" | tr -d '[:space:]'
}

# ask puts one question to the account, with the estate's token in a header
# that never reaches a command line.
ask() {
	curl -fsS --max-time 30 -H @<(printf 'Authorization: Bearer %s\n' "$2") "$api$1"
}

# pull_token is the tunnel's token, from the account: the tunnel is the one
# the estate's route to this address belongs to.
pull_token() {
	local address="$1" account key routes tunnel answer
	account="$(vault_or_value .access.account_id)"
	key="$(vault_or_value .access.api_token)"
	[ -n "$account" ] && [ -n "$key" ] || fail "the estate's template names no account or no token to ask it with"

	routes="$(ask "/accounts/$account/teamnet/routes?is_deleted=false&per_page=1000" "$key")" ||
		fail "the account would not list its tunnel routes. Is the estate converged?"
	tunnel="$(jq -r --arg net "$address/32" '[.result[] | select(.network == $net) | .tunnel_id] | if length == 1 then .[0] else empty end' <<<"$routes")"
	[ -n "$tunnel" ] || fail "the account has no one tunnel routing $address. Converge the estate first: it makes the tunnel and its route"

	answer="$(ask "/accounts/$account/cfd_tunnel/$tunnel/token" "$key")" || fail "the account would not give the tunnel's token"
	unset key
	jq -r 'if .success then .result else empty end' <<<"$answer"
}

install_tunnel() {
	[ "$(id -u)" -ne 0 ] || fail "run this as yourself, not as root: it reads the vault as you and asks for sudo where it needs it"
	local tool
	for tool in op jq curl sudo ip awk systemctl /usr/sbin/nft; do
		command -v "$tool" >/dev/null || fail "$tool is not installed on this machine"
	done

	local address token
	# The address the estate routes to the workstation, from the same
	# template the estate is converged from.
	address="$(vault_or_value .workstation.address)"
	[[ $address =~ $ipv4 ]] || fail "the estate's config gives the workstation no IPv4 address, so there is no tunnel to it to install"
	# An address this machine already has on its network is the house's to
	# hand out, and the route would break the day the router hands out
	# another.
	if ip -4 -o addr show scope global | awk '{ sub(/\/.*/, "", $4); print $4 }' | grep -qxF "$address"; then
		fail "the workstation's address in the estate's config is one this machine's network gave it. Use an address of the workstation's own, which the lock puts on loopback"
	fi

	say "asking the account for the tunnel's token"
	token="$(pull_token "$address")"
	[ -n "$token" ] || fail "the account gave no token for the tunnel"

	mapfile -t servers < <(resolvers)
	[ "${#servers[@]}" -gt 0 ] || fail "this machine has no IPv4 name server in /etc/resolv.conf, and the connector finds the vendor's edge by name"

	say "taking delivery of the connector, by hash"
	"$here/../scripts/take-delivery.sh" cloudflared >/dev/null

	say "installing as root: a user, the connector, its token, the lock and two units"
	id "$user" >/dev/null 2>&1 || sudo useradd --system --no-create-home --shell /usr/sbin/nologin "$user"
	sudo install -d -m 0755 "$libexec"
	sudo install -m 0755 "$HOME/.local/bin/cloudflared" "$libexec/cloudflared"
	sudo install -d -m 0750 -o root -g "$user" "$etc"
	# Root's, and readable by the connector's group: the connector can use
	# its token and cannot change it.
	printf '%s' "$token" | sudo install -m 0640 -o root -g "$user" /dev/stdin "$etc/token"
	unset token

	local staged
	staged="$(mktemp)"
	rules "$address" "$(id -u "$user")" "${servers[@]}" >"$staged"
	# Checked before it replaces the one in force.
	sudo /usr/sbin/nft -c -f "$staged" || fail "the lock did not pass nft's own check, so nothing was changed"
	sudo install -m 0644 "$staged" "$etc/lock.nft"
	rm -f "$staged"

	lock_unit_text "$address" | sudo install -m 0644 /dev/stdin "/etc/systemd/system/$lock_unit"
	unit_text | sudo install -m 0644 /dev/stdin "/etc/systemd/system/$unit"
	sudo systemctl daemon-reload
	sudo systemctl enable --quiet "$lock_unit" "$unit"
	sudo systemctl restart "$lock_unit"
	sudo systemctl restart "$unit"

	say "waiting for the connector to reach the vendor's edge"
	for _ in $(seq 1 30); do
		connected && break
		sleep 1
	done
	check || fail "installed, and not right. The lines above say what"
	say "installed. From an enrolled device, away from this network: ssh to this machine's address."
}

remove_tunnel() {
	sudo systemctl disable --now "$unit" "$lock_unit" 2>/dev/null || true
	sudo rm -f "/etc/systemd/system/$unit" "/etc/systemd/system/$lock_unit"
	sudo systemctl daemon-reload
	sudo rm -rf "$etc" "$libexec"
	if id "$user" >/dev/null 2>&1; then
		sudo userdel "$user"
	fi
	say "removed. The tunnel itself is the estate's: take the workstation's address out of the estate vault and converge to remove it there."
}

case "${1:-}" in
install) install_tunnel ;;
check) check ;;
remove) remove_tunnel ;;
rules)
	shift
	[ "$#" -ge 3 ] || fail "usage: $0 rules <address> <uid> <resolver>..."
	rules "$@"
	;;
*)
	echo "usage: $0 install | check | remove | rules <address> <uid> <resolver>..." >&2
	exit 2
	;;
esac
