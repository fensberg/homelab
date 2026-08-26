# Development workstation

A long-lived Linux machine on the Proxmox host, for day-to-day work.

**This is not part of the homelab workflow.** It shares no configuration with
`management/`, reads nothing from 1Password, and `scripts/ignite`
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
