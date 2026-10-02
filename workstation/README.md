# Development workstation

A long-lived Linux machine on the Proxmox host, for day-to-day work.

**This is not part of the homelab workflow.** It shares no configuration with
`management/`, reads nothing from 1Password, and `scripts/contractor`
never touches it. That separation is the point: the ignition run destroys
everything it owns when it fails, and a machine you code on for years must not
sit inside that blast radius.

It lives on the hypervisor's LAN bridge rather than the cluster network, so it
stays reachable whether or not the SDN is healthy - no jump host, no static
routes, and VS Code Remote-SSH works against it directly.

## Build it

```sh
cd workstation
cp inventory.example.yml inventory.yml     # point at your hypervisor
ansible-playbook -i inventory.yml provision.yml
```

Debian 13, 6 cores, 16 GB, 200 GB, VM ID 9000. It authorises whatever keys the
hypervisor already trusts, so if you can SSH to the hypervisor you can SSH here.
The address it gets is read back from the guest agent and printed at the end.

## Options

| Override                                         | Default | Notes                                     |
| ------------------------------------------------ | ------- | ----------------------------------------- |
| `-e vmid=9000`                                   | `9000`  | Clear of the homelab's per-site bands     |
| `-e cores=6 -e memory=16384`                     |         |                                           |
| `-e disk=200G`                                   | `200G`  |                                           |
| `-e address=ip=192.168.50.20/24,gw=192.168.50.1` | `dhcp`  | Worth pinning for a machine you use daily |
| `-e bridge=vmbr0`                                | `vmbr0` | The LAN bridge, not the SDN               |
| `-e state=absent`                                |         | Destroy it                                |

## Rebuild

```sh
ansible-playbook -i inventory.yml provision.yml -e state=absent
ansible-playbook -i inventory.yml provision.yml
```

## Cloud-init note

Extra directives go in **vendor-data**, never `--cicustom user=`. A custom
`user=` snippet _replaces_ the user-data Proxmox generates from `--ciuser` and
`--sshkeys`, so supplying one that only lists packages silently discards the
login account and its keys. The VM boots perfectly and refuses every
connection with `Permission denied (publickey)`.

## What it does not do

It installs a build toolchain and nothing else. Project tooling - OpenTofu,
`op`, `talosctl`, `kubectl`, `flux` - belongs to whatever you are working on,
not to the machine, and pinning versions here would only drift from what the
projects expect.

## Reaching it from away

The workstation has a tunnel of its own, so it can be reached from an
enrolled device anywhere without the laptop joining the overlay, and
without depending on any cluster being healthy.

- **The estate's half** is `management/estate/workstation.tf`: one tunnel,
  one route. The address it routes is `workstation.address` in
  `config/estate.tpl.json`. It is the workstation's own, held on loopback,
  not the address the house's router hands out, so it does not move with a
  DHCP lease and is not shadowed by whatever network you are on.
- **The workstation's half** is `workstation/tunnel.sh`: the connector, as a
  service under a user of its own, and a firewall rule that holds that user
  to this machine's SSH port. The connector cannot open the router, the
  hypervisor, the overlay or any other port here, whatever routes the tunnel
  is given.

Getting in takes an enrolled device and an SSH key.

Once, after the estate has been converged, with the token the lawyer runs
with:

```sh
workstation/tunnel.sh install   # asks the account for the tunnel's token; asks for sudo
```

The token is granted to nobody and kept in no vault: the install asks the
account for it with the estate's own token and writes it only where the
connector can read it.

It proves itself before it says it is installed, from the connector's own
user: the SSH port answers, and the gateway and this machine's other
addresses do not. Run the proof again whenever you like:

```sh
workstation/tunnel.sh check
```

Then, from an enrolled device on another network:

```sh
ssh <you>@198.18.0.1
```

`workstation/tunnel.sh remove` takes the workstation's half off again.
