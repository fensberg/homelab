# Epoch 01 — Ignition

- **Tier / path:** `management/`
- **Branch:** `epoch/01-ignition`
- **PR:** #9 and successors (pre-dates this log; recorded retroactively)
- **Status:** In progress

## Goal

Bring a management Kubernetes cluster into existence from bare metal, from a
Windows workstation, with one command. At the end, a three-node Talos control
plane runs on Proxmox with Flux bootstrapped against this repo — enough of a
platform that later epochs can deploy through CI instead of locally.

## Scope

In scope:

- Ansible baseline for the Proxmox host: repos, SDN, Tailscale, RBAC.
- OpenTofu: Talos ISO from the Image Factory, three control-plane VMs,
  machine config, bootstrap, kubeconfig, Flux.
- Codified Tailscale tailnet policy and route auto-approval.
- Cloudflare R2 bucket and encrypted off-site state backup.
- A single phased PowerShell entrypoint that orchestrates all of it.

Explicitly out of scope:

- Reusable modules — epoch 02. Staging/production overlays — epoch 03.

## Decisions

### Ignition runs locally, never in CI

**Chose:** one PowerShell entrypoint on the workstation.
**Because:** this epoch builds the cluster that the self-hosted runner and
Flux will later live on. Bootstrapping it from CI would be circular.

### The button is phased, and phases are individually runnable

**Chose:** nine named phases, with `-Phase` to run one and `-From` to resume.
**Rejected:** a single `tofu apply -auto-approve`, which is what the original
`deploy-local.sh` did.
**Because:** a monolithic apply reports every failure identically — as
"still creating…" — with no signal about whether the problem is Proxmox,
the network, or Talos. Phases turn a twenty-minute silent hang into a
targeted error in seconds.

### The Verify phase gates OpenTofu on network reachability

**Chose:** ping the SDN gateway and probe the Proxmox API before any apply.
**Because:** the historical failure mode of this project was Talos sitting in
maintenance mode, unreachable, while the console showed no useful error. The
cause is always one of two network problems, and both are cheap to test for.

### Failure destroys before it sterilizes

**Chose:** on error, run `tofu destroy` first, then wipe local state.
**Because:** the workspace is sterilized on every exit by design. Deleting
state without destroying first leaves VMs running that nothing tracks. The
ordering makes an aggressive cleanup policy safe. `-KeepOnFailure` opts out.

### Ansible runs inside WSL2

**Chose:** the PowerShell entrypoint shells into WSL for the playbook phase.
**Because:** Ansible has no supported Windows control node. WSL is set up by
`Install-Dependencies.ps1` with mirrored networking, so it inherits the
Windows host's Tailscale routes rather than sitting behind its own NAT.

### Tailscale route approval is codified, not clicked

**Chose:** manage the tailnet policy file with the Tailscale provider, using
`autoApprovers` keyed on `tag:homelab-router`, and mint the hypervisor's
auth key with `tailscale_tailnet_key`.
**Rejected:** approving the advertised subnet route in the admin console.
**Because:** the project's standing rule is no ClickOps. An advertised route
that needs a human to approve it is a manual step that silently blocks
ignition, and it is invisible in git.

### State is encrypted with an age *recipient*, not a passphrase

**Chose:** encrypt the R2 backup to an age public key; keep the private
identity in 1Password, offline.
**Because:** the automation only ever needs to encrypt. Giving it a
passphrase would let anything that can run the backup also read every
backup. With a recipient key, a compromised workstation can write backups
but cannot decrypt them.

### Talos pinned to an exact version *and* schematic ID

**Because:** the Factory schematic encodes the system extensions. The boot
ISO and the `installer` image must agree, or the node reboots into a
different image than it booted.

### Local state first, Postgres second, R2 third

**Chose:** apply on local state; migrate into cluster Postgres; back up an
encrypted copy off-site.
**Because:** the Postgres backend runs *on* the cluster this code creates, so
it cannot exist at first apply. R2 covers the circular dependency that
creates — losing the cluster would otherwise lose the state describing it.

## Outcome

To be completed when the epoch closes.

## Deferred

- **Postgres itself is not yet deployed.** Phase 7 fails with instructions
  until a Postgres exists on the cluster and `state.conn_str` is in
  1Password. The open decision is CloudNativePG (operator, backups, more
  moving parts) versus a plain StatefulSet (simple, you own backups).
- **OpenTofu native state encryption** (`terraform { encryption { … } }`)
  would encrypt state at rest inside Postgres, complementing the R2
  encryption. Trigger: once Postgres is real.
- **`insecure = true` on the Proxmox provider** — trigger: a trusted cert.
- **QEMU guest agent** is off deliberately; Talos will not report ready
  without the extension in the Factory schematic.

## Gotchas

- **`tailscale_acl` replaces the entire tailnet policy file.** The resource
  in `tailscale.tf` carries a permissive default ACL so applying it cannot
  lock you out, but if you have hand-written tailnet rules, port them into
  that resource *before* the first apply or they will be overwritten.
- **`kubernetes_version` in `talos.tf` is hardcoded** with no Renovate
  annotation, unlike `talos_version`. Confirm the pairing is inside the
  Talos release's supported range; it will otherwise drift silently.
- **The Proxmox role needs `Datastore.AllocateTemplate`** to download the
  Talos ISO into a datastore. Without it the ISO task fails with a 403. If
  ISO downloads work but the role lacks it, you are authenticating as
  `root@pam` rather than the service account.
- **`ifreload -a` is deliberately absent** from the playbook. It reloads
  every interface on a live hypervisor and can drop the management link.
  `pvesh set /cluster/sdn` is sufficient to commit SDN changes.
- **The SDN DHCP pool is pinned to .50–.99** so it cannot collide with the
  static control-plane addresses at .100–.102.
- **`datastore_id = "local-iso"`** is not a stock Proxmox datastore name.
  Confirm it exists with `iso` in its content types.
- **The deploy workflow never fires for this epoch.** Its path filters cover
  only `environments/**` and `modules/**`. Expected, not broken.
