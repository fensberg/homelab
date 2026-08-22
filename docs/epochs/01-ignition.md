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

### Route approval is codified, but the policy is not managed per site

**Chose:** the tailnet policy (`tagOwners`, `autoApprovers`) is set up once per
tailnet, out of band, and documented in `docs/tailnet-setup.md`. Each
deployment only mints a tagged auth key with `tailscale_tailnet_key`.
**Rejected:** managing the policy with `tailscale_acl` from this root, which is
what the first implementation did.
**Because:** `tailscale_acl` replaces the policy file wholesale, so every site
deployment would clobber the policy every other site depends on. The policy is
a property of the tailnet, not of any one deployment. Route approval is still
codified - it just lives at the layer that owns it.

**Chose:** an OAuth client rather than a stored auth key.
**Because:** auth keys expire after 90 days at most, so a pre-baked one turns
into an expired-credential failure at a client site. An OAuth client does not
expire and mints a fresh key on every run.

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

### Config is named by function, not by vendor

**Chose:** `source_control`, `overlay_network`, `object_storage`, `state` as
config keys and 1Password paths, with no `provider` field.
**Rejected:** `git`/`github_pat_reference`, `tailscale`/`tailnet`, `r2`.
**Rejected:** carrying a sibling `provider` value for readability — nothing
branches on it, and a field only humans read is a field that drifts out of
sync with reality without anyone noticing. The vendor is named in each `.tf`
file's header, where the resource names make it unambiguous anyway.
**Because:** swapping a vendor should change a value, not a schema. The limit
is honest: Terraform resource names are irreducibly vendor-specific, so this
keeps the blast radius of a swap to one `.tf` file rather than eliminating it.

### CloudNativePG for the state database

**Chose:** CloudNativePG, deployed by Flux, backing up to object storage via
`barmanObjectStore`.
**Rejected:** a plain StatefulSet or a Bitnami chart, which would have been
less work.
**Because:** the operator pattern is the transferable skill — CRDs and
reconciliation loops are how organizations actually run stateful workloads on
Kubernetes. CloudNativePG is CNCF-governed and has the broadest adoption of
the Postgres operators, so the knowledge carries outside this homelab. It also
brings replication, failover, and point-in-time recovery without hand-rolling
any of them.

### Secrets are written by OpenTofu, not committed to git

**Chose:** OpenTofu creates the namespace and secrets; Flux reconciles
everything else. Non-secret-but-not-in-git values (bucket name, account
endpoint) reach the manifests through Flux `postBuild.substituteFrom`.
**Because:** Flux reconciles from git, so a password cannot live in a
manifest. SOPS and External Secrets are each their own epoch of work. Until
then OpenTofu already holds the 1Password-rendered values and ignition is a
local, human-run operation, so it is the natural place for this.

### One fleet document, held in the vault

**Chose:** the whole topology lives in a single 1Password field at
`op://homelab/topology/fleet`. It maps a site name to an octet, a control-plane
count, and the hypervisors that site owns. Everything else - the advertised
/16, node subnet, gateway, DHCP pool, node addresses, VM placement and cluster
name - derives from that entry, in both OpenTofu and the start button.
**Rejected:** literals in the code, and a registry committed to git.
**Because:** two reasons, and the second changed the answer.

First, the values are not independent. A literal invites two sites onto the
same subnet, which collides on the tailnet and presents as a broken network
rather than a configuration mistake. Deriving them from one octet makes that
impossible to express.

Second, for an MSP the topology *is* sensitive: client names, network ranges
and hypervisor addresses are reconnaissance material. A registry in git leaks
the shape of every client estate to anyone who reads the repository. The vault
is the right home, and `config/fleet.example.json` documents the shape without
carrying any of it.

The cost is that topology changes lose version history and code review. That is
a real loss, accepted because the alternative publishes client infrastructure.

**Consequence:** the Ansible inventory is generated rather than templated.
`op inject` substitutes into a fixed file and cannot loop, so a template could
never grow with the fleet. Generating it is what makes appending a hypervisor
genuinely sufficient.

### The connection string is derived, not stored

**Chose:** build `state_conn_str` in OpenTofu from the owner, database name,
NodePort and address already declared in `variables.tf`, plus the password
from 1Password.
**Rejected:** a `state-database/conn_str` item in the vault.
**Because:** storing it invents a chicken-and-egg problem - you cannot record
a connection string for a database that has not been created yet - and buys
nothing, since every component is known in advance. The password is the only
part that is genuinely secret, so it is the only part stored.

### Local state first, Postgres second, object storage third

**Chose:** apply on local state; migrate into cluster Postgres; back up an
encrypted copy off-site.
**Because:** the Postgres backend runs *on* the cluster this code creates, so
it cannot exist at first apply. R2 covers the circular dependency that
creates — losing the cluster would otherwise lose the state describing it.

## Outcome

To be completed when the epoch closes.

## Deferred

- **A default StorageClass may not exist on Talos yet.** CloudNativePG will
  request 10Gi per instance and the pods will pend forever if nothing can
  satisfy the claim. Trigger: the first reconcile of the state database.
- **OpenTofu native state encryption** (`terraform { encryption { … } }`)
  would encrypt state at rest inside Postgres, complementing the R2
  encryption. Trigger: once Postgres is real.
- **`insecure = true` on the Proxmox provider** — trigger: a trusted cert.
- **QEMU guest agent** is off deliberately; Talos will not report ready
  without the extension in the Factory schematic.

## Gotchas

- **The tailnet policy is not managed by this code.** If ignition hangs on an
  unreachable node, confirm `docs/tailnet-setup.md` has actually been applied
  to the tailnet you are deploying into. A missing `autoApprovers` entry looks
  identical to a broken network.
- **The SDN zone is `simple`, which is node-local.** Each Proxmox node gets
  its own isolated bridge carrying the same subnet, so VMs on different nodes
  cannot reach each other. That is correct for one hypervisor and breaks the
  moment a second joins the Proxmox cluster. Multi-node needs a `vxlan` or
  `evpn` zone instead - see epoch 02.
- **Addressing comes from `the fleet document`, never from a literal.** The
  site octet is that site's identity on the overlay network. Retire an octet
  when a site goes away; do not recycle it, because lingering routes for the
  old site would start pointing at the new one.
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
