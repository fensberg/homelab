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

### State is encrypted with an age _recipient_, not a passphrase

**Chose:** encrypt the R2 backup to an age public key; keep the private
identity in 1Password, offline.
**Because:** the automation only ever needs to encrypt. Giving it a
passphrase would let anything that can run the backup also read every
backup. With a recipient key, a compromised workstation can write backups
but cannot decrypt them.

### Talos pinned to an exact version _and_ schematic ID

**Because:** the Factory schematic encodes the system extensions. The boot
ISO and the `installer` image must agree, or the node reboots into a
different image than it booted.

### Config is named by function, not by vendor

**Chose:** `source_control`, `overlay_network`, `object_storage`, `state` as
config keys and 1Password paths, with no `provider` field.
**Rejected:** `git`/`github_pat_reference`, `tailscale`/`tailnet`, `r2`.
**Chose:** a sibling `provider` field on each vendor-locked concern, asserted
at plan time in `registry.tf`.
**Rejected, then reinstated:** it was first dropped as documentation that
nothing reads and that drifts out of sync. That was wrong. As an _assertion_
it earns its place: the code in this root speaks to exactly one vendor per
concern, and without the check, pointing the vault at S3 credentials while the
code still calls Cloudflare produces an opaque authentication failure instead
of "this config declares cloudflare". A declaration the code verifies cannot
drift, because drifting fails the plan.

It is added only where the code is genuinely vendor-locked - hypervisor,
overlay network, object storage. `source_control` has none, because
`flux_bootstrap_git` speaks plain git over HTTPS and works against GitHub,
GitLab or Gitea alike; asserting a vendor the code does not depend on would be
noise rather than a guard.
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

### The vendor is attested by the vault, not just declared in git

**Chose:** each vendor-locked concern carries two provider values - `provider`
declared in git, and `vault_provider` read from the 1Password item - plus a
shape check on the object-storage access key. `registry.tf` asserts the code's
requirement, the config's declaration and the vault's attestation all agree.
**Rejected:** the first version, which compared only the config's declaration
against what the code implements.
**Because:** those are both in git and both change in the same commit, so the
check could never fail in a way that mattered. It could not see the failure it
was written for: someone replacing a 1Password item's contents with another
vendor's credentials while every file in the repository stays untouched. The
plan would pass and the credentials would be thrown at the wrong API.

Putting the attestation in the vault makes the declaration travel with the
thing it describes. Swapping the item's contents without updating its provider
field is then the only way through - so a shape check covers that too: an
access key beginning `AKIA` or `ASIA` is an AWS credential, while R2 issues 32
hex characters, which is positive identification rather than a heuristic.

### The octet is declared, not computed

**Chose:** each site carries an explicit `octet`. It picks the site's network
(`10.<octet>.0.0/16`), names the site (`site10`), names its VMs
(`site10-cp-01`) and bands its VM IDs (1000-1099).
**Rejected:** deriving the octet from the array index, which the previous
attempt did.

Deriving it made collisions inexpressible, which sounds strictly better and is
not. It bought that guarantee by making array position load-bearing: reordering
`sites[]` silently repointed an estate at a different network, retiring a site
meant renumbering every site after it, and reading the config told you nothing
about the network without doing arithmetic first.

Declaring it costs one assertion - `registry.tf` checks uniqueness and range
across every site, not just the selected one - and buys a config you can read,
gaps you can leave, and an array you can safely reorder. For a structure meant
to be obvious at a glance, that is the better trade.

Names follow the octet rather than the array position for the same reason:
`site10-cp-01` lives at `10.10.10.100`, which lines up when reading Proxmox or
debugging a route, and stays stable however the array is ordered.

### Vendor and credentials live inside the site

**Chose:** `hypervisor`, `overlay_network`, `object_storage` and `state` all
sit inside each `sites[]` entry, each carrying its own asserted `provider`.
Only `organization` and `source_control` stay fleet-wide.
**Because:** the test is whether the thing describes one estate or the whole
fleet. A hypervisor obviously belongs to a site. So does the overlay network,
once sites can join different tailnets depending on the engagement. So does
object storage, when a client's backups belong in a client's bucket. And so
does state: each site runs its own cluster with its own Postgres, so a shared
database password would mean compromising one site reaches every other.

One repository drives every cluster through Flux, so `source_control` is
genuinely fleet-wide and stays at the top.

**Note:** `sites[].name` is a vault reference. The human label for a site -
which may be a client's name - never reaches git, while the positional
identity (`site0`) that drives addressing stays visible and reviewable.

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
**Because:** the Postgres backend runs _on_ the cluster this code creates, so
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

- **Renaming a site renames its VMs.** Everything nameable derives from
  `sites.<key>.name`, so changing it in the vault makes OpenTofu see different
  VMs and destroy and recreate them. Rename deliberately, not casually.
- **The WSL command is written to a file, not passed as `bash -lc`.** WSL
  inherits the Windows PATH, which contains `Program Files (x86)`, and an
  unquoted assignment of it is a bash syntax error at the parenthesis. A
  script file sidesteps every layer of quoting between PowerShell, wsl.exe
  and bash.

- **Tailscale auth key descriptions reject punctuation.** The API returns
  "description had invalid characters (400)" for parentheses; the field is
  also capped at 50 characters. Letters, digits and spaces only.

- **The age private key is never referenced by the automation, and must not
  be.** The config contract carries only `backup_recipient`, the public half,
  so the start button can write backups and cannot read them. Putting the
  identity in `management.tpl.json` would render it to disk on every run and
  hand every historical backup to anyone who compromised the workstation. It
  lives in 1Password as `backup_identity` and is fetched by a human, by hand,
  only when restoring.
- **Rotating the age key orphans every existing backup.** Backups are
  encrypted to a recipient, so a new key pair cannot read anything written
  under the old one. Rotate before real backups exist, or decrypt and
  re-encrypt the archive deliberately.

- **The tailnet policy is not managed by this code.** If ignition hangs on an
  unreachable node, confirm `docs/tailnet-setup.md` has actually been applied
  to the tailnet you are deploying into. A missing `autoApprovers` entry looks
  identical to a broken network.
- **The SDN zone is `simple`, which is node-local.** Each Proxmox node gets
  its own isolated bridge carrying the same subnet, so VMs on different nodes
  cannot reach each other. That is correct for one hypervisor and breaks the
  moment a second joins the Proxmox cluster. Multi-node needs a `vxlan` or
  `evpn` zone instead - see epoch 02.
- **Retire an octet, never recycle it.** Routes for a decommissioned site can
  linger on the tailnet, and reissuing its octet points them at the new estate.
  Leave gaps; the range is 1-95 and you will not run out.
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
