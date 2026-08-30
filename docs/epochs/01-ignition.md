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

**Superseded by "The entrypoint moves from PowerShell to Go" below**, once
`workstation/` made a Linux dev machine the norm. Left here rather than
deleted - it documents a real problem this project actually had, and the
WSL-specific gotchas later in this record explain failures anyone using an
old clone or old habits could still hit.

### The entrypoint moves from PowerShell to Go

**Chose:** port `Start-Homelab.ps1` and `Install-Dependencies.ps1` to a Go
program (`scripts/steward`) plus a plain bash bootstrap
(`scripts/install-dependencies.sh`), run from the Linux devbox
`workstation/` provisions rather than from Windows.
**Rejected:** Python, which needed no new toolchain (the devbox already runs
it for Ansible) and would have been less code to write.
**Because:** the project's purpose is learning production-grade patterns,
not shipping the least-effort implementation. Every other tool this root
depends on - OpenTofu, Talos, Flux, kubectl - is a Go binary, and so is the
rest of the ecosystem this project's choices already model themselves on
(Helm, Argo CD, cluster-api, Vault). That is the same reasoning that picked
CloudNativePG over a plain StatefulSet: the operator pattern, not the
shortest path, is the transferable skill. Go was the closer match to what
this project is actually for.

Moving off Windows also deleted a whole layer rather than porting it: the
WSL2 hop, its path translation, and the `ANSIBLE_CONFIG` workaround for a
world-writable `/mnt/c` all existed solely because Ansible has no supported
Windows control node. Ansible now runs natively, and `ansible.cfg` is picked
up by the same ambient discovery `check-hypervisor` already relied on.

The bootstrap script stayed bash on purpose: `scripts/steward` needs Go to
run, so whatever installs Go cannot itself depend on Go already being
present.

While reading the original script to port it, two live bugs surfaced and
were fixed in the same pass, independent of the language: `.gitignore`
excluded a file named `tailscale.auto.yml`, but the Overlay phase has always
written the Tailscale auth key to `overlay-network.auto.yml` - that file was
never actually gitignored. And `config/management.tpl.json`'s
`overlay_network.domain` field was missing the `{{ }}` template braces
`op inject` requires, so it would never have been substituted on a real
render.

### `task start` builds ignite but does not run it

**Chose:** `task start` runs `go build` and then prints the command to run
`./scripts/steward/steward` directly. Every other ignite-invoking task
(`render-secrets`, `verify`, `configure-hypervisor`, `backup-state`,
`clean-secrets`) still execs the binary through `task` as normal.
**Rejected:** the original design, where `task start` ran ignite directly,
same as every other phase task.
**Because:** discovered the hard way, on the second real hardware run.
Ignite's whole safety model - destroy before sterilize, on any failure -
depends on catching Ctrl-C and running that cleanup. It had never been
signal-aware at all (fixed in the same pass: `main.go` now races phase
execution against `os/signal.Notify(syscall.SIGINT, syscall.SIGTERM)` and
treats an interrupt exactly like a returned error). But adding that turned
out not to be enough on its own: `task` itself intercepts SIGINT for its own
purposes and does not proxy it to the subprocess it's supervising - a
confirmed, currently-open upstream limitation
(`github.com/go-task/task/issues/1408`). Verified directly: a signal-aware
binary invoked straight from a shell catches Ctrl-C and runs its cleanup
every time; the identical binary invoked through `task` does not - the
child dies before its handler ever runs. No amount of code in `ignite` can
fix a signal that `task` never delivers.

The blast radius of skipping this: a Ctrl-C during a `task start` run left
three real VMs orphaned - Terraform's state believed their Talos machine
config had applied successfully; the live nodes had no network config at
all and never joined etcd. State and reality had diverged in a way that
was not safe to reconcile forward, only to destroy and rebuild.

Every other ignite-invoking task stays wrapped in `task` because none of
them can reach the Compute phase on their own, so the same failure mode
just leaves stale secrets on disk - recoverable with `task clean-secrets`,
never an orphaned VM. Only the one command that can create real
infrastructure needed to change.

### EmergencyDestroy waits for the interrupted step to actually exit first

**Chose:** on interrupt, block on the phase goroutine's result a second time
before calling `EmergencyDestroy` - not just react to the signal and proceed
immediately. `EmergencyDestroy` itself now reports whether the destroy
actually succeeded, and `main` only runs Sterilize when it did.
**Rejected:** the first version of the interrupt fix (above), which reacted
to the signal and called `EmergencyDestroy` right away.
**Because:** proven wrong on the very next real interrupt. `tofu` runs its
own graceful shutdown after SIGINT ("Gracefully shutting down... Stopping
operation...") that takes real time to release its state lock. Racing
straight to a fresh `tofu destroy` hit "Error acquiring the state lock"
every time, because the original interrupted apply still held it. Worse,
the old code ran Sterilize regardless of whether destroy succeeded - so a
failed destroy was immediately followed by deleting the only state that
could have retried it. That combination orphaned three real VMs with no
state left to destroy them through; recovering meant destroying them by
hand directly against the Proxmox API, bypassing Terraform entirely.
Waiting for the original subprocess to genuinely exit before attempting
anything, and refusing to sterilize after a destroy that failed, closes
both halves of that failure at once.

### The persisted Talos machine config had no network section

**Chose:** three separate multi-document config patches - `LinkConfig` for
the static address and default route, `ResolverConfig` for nameservers, and
`HostnameConfig` (`auto: "off"` plus `hostname`) for the hostname - none of
them touching the legacy `machine.network` block at all.
**Rejected, in order:**

1. Leaving networking entirely to Proxmox cloud-init, which is what every
   prior version of this config did.
2. Setting `hostname` inside a monolithic `machine.network` block. Talos
   1.12+ generates its own `HostnameConfig` document by default
   (`auto: stable`), and a `machine.network.hostname` field collides with it
   outright: "static hostname is already set in v1alpha1 config."
3. Setting the address/routes/nameservers the same monolithic way, believing
   they were fine because they produced no error anywhere in the pipeline -
   generation or apply. They were not fine: `NetworkConfig`, the struct
   backing all of `machine.network.*`, is fully deprecated
   (`pkg/machinery/config/types/v1alpha1/v1alpha1_types.go`, this pinned
   tag: "all fields in NetworkConfig are deprecated, use corresponding
   multi-doc config types instead"). It accepted the config and silently
   didn't apply the address. A node that never gets a real address is
   indistinguishable, from the outside, from one that's just slow to
   reboot - the second real hardware attempt burned a full cycle on exactly
   that ambiguity.

**Because:** cloud-init's `ip_config`/`dns` (in `compute.tf`) only feeds
Talos's ephemeral nocloud network bring-up before any real machine config
exists - it has no bearing on what Talos actually persists. A node would
boot with a correct address from cloud-init, then lose it permanently the
moment the real machine config applied, because nothing it understood as
current API described a replacement. This subnet's DHCP pool is pinned to
`.50-.99` (see the SDN gotcha below), which does not cover the `.100+`
control-plane range, so the failure mode was never "gets a different
address" - it was silent, permanent, total loss of connectivity.

Getting `HostnameConfig` right took two attempts on its own: patches merge
field-by-field into the generator's own document rather than replacing it,
so adding `hostname` alone left the generator's `auto: stable` sitting right
next to it - rejected as "'auto' and 'hostname' cannot be set at the same
time." `auto = false` (tried next) isn't a valid enum member - `Validate()`
in `pkg/machinery/config/types/network/hostname.go` only accepts `"stable"`
and `"off"`, and the "conflict" only actually fires when `auto` is anything
other than exactly `"off"` - the two aren't mutually exclusive the way the
prose docs imply.

**The process failure worth recording alongside the technical one:** every
wrong guess above except the last was made by triangulating web search
summaries and third-party blog posts - some of which directly contradicted
each other - against a live Talos node, at the cost of a full VM
provisioning cycle each time. The actual answer, both times, was sitting in
`github.com/siderolabs/talos` at the exact pinned tag, one `gh api` /
GitHub code search away, and unambiguous the moment it was read. `talosctl`
(already on the workstation) generates and validates real multi-document
configs completely offline via `talosctl gen config` /
`talosctl validate` - every patch in this decision was proven against that,
against source, before it ever touched hardware again.

### The Tailscale login retries itself

**Chose:** wrap the `tailscale up` task in Ansible's own `until`/`retries: 2`/
`delay: 15`, so a control-plane blip is absorbed inside the same playbook run.
**Rejected:** the original design, which bounded the login with `--timeout`
and then treated a failure as the operator's problem to notice and fix by
re-running the whole start button.
**Because:** discovered on the first real run against hardware - the
coordination server hiccuped, `--force-reauth` fired mid-login, and the fix
really was just "run it again," but making a human be the retry loop is not
what "safe to re-run" was supposed to mean in practice. The retry is safe
specifically because of the recoverable-logout property already documented
above: a logged-out host takes the plain login path next attempt, so retrying
in place is never worse than the failure it is recovering from. Bounded at 3
attempts total rather than retried forever, so a genuinely unreachable
coordination server still fails loudly with the existing diagnostics instead
of hanging the playbook indefinitely.

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
of "this config declares `cloudflare`". A declaration the code verifies cannot
drift, because drifting fails the plan.

It is added only where the code is genuinely vendor-locked - hypervisor,
overlay network, object storage. `source_control` has none, because
`flux_bootstrap_git` speaks plain git over HTTPS and works against GitHub,
GitLab or Gitea alike; asserting a vendor the code does not depend on would be
noise rather than a guard.
**Because:** swapping a vendor should change a value, not a schema. The limit
is honest: Terraform resource names are irreducibly vendor-specific, so this
keeps the blast radius of a swap to one `.tf` file rather than eliminating it.

### The overlay network is remote access, not cluster plumbing

**Chose:** the overlay network is optional. `-SkipOverlay` drops the Overlay
phase and tells the playbook to leave Tailscale alone; the SDN, its gateway
and its SNAT are configured either way.
**Rejected:** the original arrangement, where the playbook always configured
Tailscale and the Verify phase always reached the SDN across it.

**Because:** the two are unrelated and welding them together made a remote
access problem block cluster provisioning outright. The SDN is entirely local
to the hypervisor - the cluster does not care whether anyone can reach it from
elsewhere. The overlay exists so an operator who is _not_ on that LAN can.
Tying them meant a wedged tailnet stopped VMs being created, which is
backwards.

On the hypervisor's own LAN the overlay buys nothing for provisioning: one
static route on the workstation, pointing at the hypervisor, reaches the SDN
directly and depends on nothing but the LAN.

    route -p add 10.10.0.0 mask 255.255.0.0 <hypervisor LAN address>

**Only on the same subnet.** A static route's next hop must be on-link. Name a
gateway that is not, and Linux creates an implicit on-link entry for it - so
the workstation starts ARPing for an address nobody answers and loses the
routed path it already had to that host. It breaks reachability rather than
adding it. From another subnet, use the overlay network, or add the route on
the router that joins them.

This is also the shape client work wants. Provision on site over the LAN; the
overlay is how you get back in afterwards. If it is down you have lost remote
access, not the ability to build anything.

### The SDN zone is EVPN from the start, not simple

**Chose:** an EVPN zone with a BGP controller, from the first deployment,
with peers and exit nodes derived from the inventory.
**Rejected:** a `simple` zone, which is what the first implementation used and
what shipped for several iterations.
**Because:** a simple zone is node-local. Every hypervisor gets its own
isolated bridge carrying the same subnet, so VMs on different nodes cannot
reach each other. It works for exactly one box and breaks silently when a
second joins - the Proxmox cluster forms and then fails to talk to itself.

That was known and written down as a gotcha rather than fixed, which was the
wrong call. Recording a landmine is not the same as not laying one, and the
migration only ever gets more expensive: a zone's type cannot be changed in
place, so converting after VMs exist means deleting the vnet and zone,
detaching every VM attached to them. Doing it while the fleet is empty costs
nothing.

`vxlan` was rejected as the replacement because it provides L2 across nodes
but no gateway and no SNAT, both of which this design depends on. EVPN is the
like-for-like substitute.

The identifiers - ASN, VRF VNI and vnet VNI - are derived from the site's
octet, so two sites cannot collide on BGP or VXLAN any more than they can on
addressing. EVPN also needs FRR, which a simple zone does not, so the playbook
installs and starts it.

**The cost:** an EVPN zone does not support Proxmox's built-in dnsmasq DHCP -
the API rejects the property outright. Nothing here needed it. Talos nodes take
static addresses from cloud-init and pods get theirs from the CNI, so the
subnet exists for its gateway and SNAT. The dnsmasq package and the DHCP pool
were removed along with it rather than left as decoration.

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

### Longhorn for cluster storage

**Chose:** Longhorn, deployed by Flux like every other controller, backing
CloudNativePG's PersistentVolumeClaims. Talos's disk image gained two system
extensions for it (`siderolabs/iscsi-tools`, `siderolabs/util-linux-tools`,
both Longhorn's own documented Talos prerequisites), each control-plane node
gained a second disk, and a `UserVolumeConfig` mounts it at
`/var/mnt/longhorn`, matching Longhorn's documented Talos data path.
**Rejected:** Rook/Ceph, the more traditionally "enterprise" answer for
distributed on-prem Kubernetes storage. **Rejected:** a hypervisor-level CSI
(a Proxmox CSI plugin backed by `local-zfs`), which is also a legitimate,
common production pattern.
**Because:** both are real, and the choice is about which one fits a fleet
that is one hypervisor today and heading toward several, run without a
dedicated storage team. Ceph is proven at large scale but wants a real
minimum node count and a non-trivial resource footprint before it is worth
running - overkill here. A hypervisor CSI ties every PersistentVolume to
`local-zfs` on whichever single Proxmox host provisioned it, which is no
better than what CNPG's PVC already had - it does not gain real redundancy
until Proxmox's own storage layer (Ceph or ZFS replication) is also built,
a separate and larger undertaking. Longhorn replicates across whichever
Kubernetes nodes it schedules to, entirely independent of which physical
hypervisor they live on - the property that actually matters the moment a
second hypervisor exists, which is the direction this fleet is already
headed. `defaultReplicaCount: 2` reflects the honest current state: two
replicas is what three control-plane nodes on one hypervisor supports
without every replica landing on the same underlying disk regardless; raise
it once a second hypervisor is real.

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

**Amended by "The object storage account plane is not the site plane" below**,
which applies this same test to two `object_storage` fields that fail it.

### The object storage account plane is not the site plane

**Chose:** split `object_storage` across both planes rather than leaving it
whole inside the site. `account_id` and `admin_token` move up to the fleet;
`bucket`, `access_key_id`, `secret_access_key` and the vendor attestation stay
in the site.

The attestation staying put is narrower than this decision first said, and the
implementation is what corrected it: `provider`/`vault_provider` attests the
credentials it travels with, and those are exactly the fields that did not
move. Duplicating it onto a two-field account block would assert the same
vendor twice from within one commit, which is the failure mode "Declare the
vendor three times" already warns about - two declarations that always change
together prove nothing.
**Because:** the rule above is right and was applied too coarsely. The test is
whether a thing describes one estate or the whole fleet, and applying it field
by field rather than block by block splits `object_storage` in two:

| Field                                | Describes              | Plane |
| ------------------------------------ | ---------------------- | ----- |
| `account_id`                         | the Cloudflare account | fleet |
| `admin_token`                        | the Cloudflare account | fleet |
| `bucket`                             | one estate's backups   | site  |
| `access_key_id`, `secret_access_key` | access to that bucket  | site  |

**What made this visible** rather than theoretical: the admin token was
re-issued with `Account API Tokens Write` so that per-run R2 credentials can
be minted, and the Cloudflare console names its own scope plainly - "Entire
Fensberg / Lemberg account". A credential that says _entire account_ on its
face, filed under `sites.site0`, claims a blast radius it does not have.

That misfiling is not cosmetic. The whole reason the database password sits
inside the site is that compromising one site must not reach every other. An
account-scoped token stored on the site plane inverts exactly that: reading
`op://homelab/site0/object_storage/admin_token` yields the account, and with
it every other site's bucket - and now the ability to mint further tokens.
Filing it at the fleet level does not reduce its power, but it stops the
layout implying a containment that was never there, and it puts one item in
front of anyone deciding how far to trust it.

**Rejected:** narrowing the token instead of moving it. There is no
site-scoped or bucket-scoped form of `Account API Tokens Write` - it is
account-scoped by construction, because minting tokens is an account
operation. The choice is to hold that power or to give up generated
credentials, not to hold a smaller version of it. Given that the credential it
generates is the one that lands in OpenTofu state, and a leaked state file is
this estate's worst case, holding it is the better trade - see
[`state-and-secret-rotation.md`](../state-and-secret-rotation.md).

**Account-owned, not user-owned, and that is the right choice.** The token is
`cfat_`-prefixed; Cloudflare's user tokens are `cfut_`. An account-owned token
is a service principal in its own right, so the integration keeps working
after the person who created it loses access - which is what a token driving
unattended ignition runs has to do. The prefix is also deliberately scannable,
so a leak is detectable by credential scanners, which is why the shape check
below is worth having.

**Two weakenings accepted knowingly.** The token has no expiry and no IP
filter. IP filtering is not workable - ignition runs from a workstation and
later from a self-hosted runner, neither on a stable address - but the absence
of an expiry is a real gap rather than an unavoidable one, and it makes this
the highest-value credential in the estate. It belongs in the rotation
runbook, not in a comment.

**Consequence for the shape check.** `registry.tf` already positively
identifies a wrong-vendor object storage key by its `AKIA`/`ASIA` prefix. The
same trick now applies to the admin token: it must begin `cfat_`. That catches
two mistakes the vendor attestation cannot - a user-owned `cfut_` token pasted
in, which would silently reintroduce the durability problem account-owned
tokens exist to solve, and a legacy-format token with no prefix at all.

**Migration, in this order.** The template must not reference a vault path
that does not exist yet, so the vault moves first:

1. Move the item to `op://homelab/object_storage/{account_id,admin_token}` and
   leave the per-site `bucket`, `access_key_id`, `secret_access_key` alone.
2. Move the two fields in `config/management.tpl.json`, `config.go` and every
   fixture; `registry.tf` and `contract_test.go` are the pair that must agree.
3. `steward check-vault` confirms the new references resolve before any run
   commits to anything - which is the case this check was built for.

**Buckets stay per-site, and gain a human step.** One bucket per site is the
isolation boundary that survives all of this, and an Object-scoped R2
credential can only name a bucket that already exists - while OpenTofu is what
creates it. So the bucket-scoped credential is minted after its bucket, not
before, whether by hand or eventually by the automation this token unblocks.

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

### The restore path was exercised, not assumed

**Drilled 2026-08-30, on the first ignition of `site0`.** The Backup phase
reports `[ok]` on rclone's exit code, and `pruneOldBackups` independently
re-lists the bucket to confirm the object arrived. Neither answers the question
the backup exists for, which is whether the bytes in the bucket can be turned
back into state by the people who would need them. That was checked directly:

    rclone copyto R2:<bucket>/management-cluster/latest.tfstate.age <drill>/latest.age
    op read op://homelab/state_backup/identity                    > <drill>/id.key
    age -d -i <drill>/id.key <drill>/latest.age                   > <drill>/state.json
    jq -r '.version, .serial, (.resources | length)' <drill>/state.json

Result: `4`, `1`, `19` - a schema version, a serial, and nineteen resources.
That is the whole chain in one pass: the object is in the bucket, the private
identity is in the vault at the path `-restore` expects, `age` accepts the
pair, and what falls out is state describing a real estate rather than bytes
that merely decoded.

**Three things make this worth repeating rather than recording once.** The
identity is deliberately absent from `config/management.tpl.json`, so
`-check-vault` cannot see it and a clean 20-of-20 says nothing about it. The
Secrets phase only warns when the recipient exists without the identity, so a
run that has already lost the private half still ends in `Ignition complete`.
And the pair is stored by hand, once, for the whole estate, which means
nothing in this repository would notice the two halves drifting apart.

**Do not use `steward restore` as the drill.** It pushes what it recovers
through the encrypted backend, and after the Migrate phase that backend is the
live cluster's Postgres. The drill above is read-only by construction: it never
runs `ignite` and it touches nothing the estate is using. Decrypt into a
`mktemp -d` under a home directory rather than `/tmp`, since the plaintext is
every secret in the estate for as long as it exists, and `shred -u` it
afterwards.

## Compute boots from a pre-installed disk image, not an installer ISO

**Status: resolved.** Every control-plane node was losing network
connectivity permanently, within seconds, the moment Talos actually
installed itself to disk - independent of which config was being applied,
independent of Talos version, independent of everything this epoch tried in
`talos.tf`.

**What's ruled out, each confirmed directly rather than assumed:**

- The `LinkConfig`/`ResolverConfig`/`HostnameConfig` patches this epoch
  settled on (see the decision above) - field-for-field identical to Talos's
  own official "Static Addressing" documentation, decoded correctly by
  `talosctl validate --strict` offline.
- Interface name (`eth0`) and disk name (`/dev/vda`) - both confirmed live
  against real hardware via `talosctl get links` / `talosctl get disks`, not
  assumed from convention.
- Reboot/install timing - the address is still missing even after a
  _verified complete_ disk-boot cycle (uptime reset to seconds, `TYPE:
controlplane` confirmed, meaning it genuinely booted from the installed
  disk, not the ISO).
- A `DHCPv4Config` workaround some users report needing alongside
  `LinkConfig` - tested directly, no effect.
- Talos version - reproduced identically, on a from-scratch VM with a single
  `apply-config` call, on both currently-supported minors: `v1.13.8` and
  `v1.13.9`.

**What's confirmed, by direct isolation:** applying the exact same
`LinkConfig` to a node where install _cannot_ succeed (an intentionally
invalid disk/image target) leaves the address stable indefinitely. Applying
it where install _does_ succeed destroys it within seconds, every time. The
install process itself is what's corrupting the network state - not
anything specific to how this project configures networking. Talos's own
`block.VolumeManagerController` fails repeatedly during this with `error
evaluating disk locator: no such attribute(s): system_disk`, an internal CEL
expression evaluation bug, not anything user-configurable.

**Why, structurally:** Talos 1.13.0 introduced a `LifecycleService` API for
installs, with an explicit disk argument and proper sequencing (pull
installer, install, drain, reboot). Per Sidero's own Omni changelog, Omni
`v1.9.0`/`v1.10.0` deliberately moved _away_ from letting a plain config
apply implicitly trigger install - which is exactly the mechanism this
project's `talos_machine_configuration_apply` resource (and every manual
`talosctl apply-config` test run here) depends on. Confirmed directly:
there is no way to invoke the modern path against an unmanaged,
maintenance-mode node through plain `talosctl` - `--insecure`, required to
reach maintenance mode at all, explicitly forces the deprecated fallback
(`talosctl upgrade --insecure` prints its own deprecation warning saying
so). That orchestration currently appears to live inside Omni, Sidero's
commercial control-plane product, not in the open CLI/Terraform surface
this epoch uses.

**Resolved:** `compute.tf` now builds VMs from a pre-installed disk image
(`nocloud-amd64.raw.xz`) instead of the installer ISO, the same pattern
`workstation/provision.yml` already uses for its Debian cloud image. Talos
is already installed the moment the VM boots, so there is no install-time
transition left to corrupt anything, and `talos.tf`'s `config_patches` carry
no `machine.install` section any more - there is nothing left for it to do.
Proven first as a throwaway VM outside Terraform entirely (`qm importdisk`

- a single `apply-config` with no install section): address stable, node
  fully reachable and authenticated, confirmed repeatedly over time - before
  touching real code.

Two things worth knowing about the Terraform side of this:

- Image Factory serves the disk image `xz`-compressed. The `bpg/proxmox`
  provider's own docs are explicit that a compressed image cannot use
  `import_from` - it needs `file_id` with `content_type = "iso"`, and
  Proxmox's zstd decompressor transparently handles the `xz` stream despite
  the mismatched name.
- `proxmox_virtual_environment_download_file` is deprecated in favor of
  `proxmox_download_file` as of this pinned provider version (`~> 0.111.0`)
  - already renamed here rather than knowingly building on something flagged
    for removal before v1.0.

### EmergencyDestroy migrates state back to local before destroying

**Chose:** when state currently lives in Postgres, `EmergencyDestroy`
reconstructs the connection string from the rendered config (not by reading
it back as a Terraform output - that requires the backend already reachable,
which cannot be assumed here) and runs `tofu init -migrate-state` back to
local before attempting the actual destroy.
**Rejected:** destroying directly against the pg backend, which is what this
function did until a real destroy hit exactly the failure this predicts:
Terraform destroys in dependency order, so the Kubernetes-hosted resources
(including the database backing this very state) are torn down before the
VMs are reached. The moment that database disappears, Terraform can no
longer record the destroy's own progress, and the run is stranded with
infrastructure still running and no way for the tool to see it.
**Because:** a state backend that lives inside the infrastructure it
describes has to leave that infrastructure before destroying it, or the
destroy cannot outlive its own bookkeeping. This was found by hand once (a
manual `tofu destroy` against a live pg-backed cluster left four VMs running
with no state anywhere) and is now something `EmergencyDestroy` does before
every full-run teardown that needs it, not something a human has to remember
to do first.

### Self-healing import blocks for orphan-prone resources

> **Superseded.** This decision described `import` blocks on both orphan-prone
> resources. That can never work for the one in the Compute phase, and the
> reasoning below survives only as the description of a problem that still
> needs solving. See the entry after it.

**Chose:** `import` blocks on `proxmox_download_file.talos_disk_image` and
`cloudflare_r2_bucket.homelab`, adopting whatever already exists at their
deterministic path/name instead of failing to create a duplicate.
**Rejected:** leaving both resources to fail loudly on the next apply if a
prior run's teardown left either one behind - which is exactly what happened
twice in one session (the Proxmox ISO with "created outside of Terraform",
then the R2 bucket with "already exists, and you own it"), each requiring a
human to diagnose and intervene by hand.
**Because:** "tear down is not always reliable" is a real, recurring
property of this project's own automation, not a hypothetical - two
independent resource types hit the identical failure mode in the same day.
Both blocks are no-ops once the resource is already in state, so they cost
nothing on a normal run; they only matter on the one that would otherwise
need a person to notice and fix it by hand.

### Correction: import cannot run before the cluster exists

**Chose:** delete the orphaned Talos disk image and let the apply re-download
it; keep adopting the R2 bucket, but materialise
`talos_cluster_kubeconfig.this` with a targeted apply first.
**Because:** `tofu import` configures **every** provider in the root, not only
the one owning the resource being imported. `versions.tf` configures the
kubernetes provider from `talos_cluster_kubeconfig` attributes that do not
exist until the cluster is built, so an import during Compute fails with
"Invalid provider configuration" pointing at `versions.tf` - before it ever
reaches the resource in question. Confirmed against the real estate: a
`-target`ed plan of that same resource succeeds, because targeting configures
only the providers the target needs; an import of it does not.
**Rejected:** leaving apply to tolerate the pre-existing file - tested, and it
fails with "refusing to override existing file", which is exactly why the
adopt existed. And deleting the R2 bucket the way the disk image is deleted,
because one is derived data worth two minutes of download and the other holds
the backups.
**Note:** the underlying cause is a provider configured from a resource in its
own root, which is the design constraint recorded in
[`02-abstraction.md`](02-abstraction.md).

### Storage: OpenEBS Local PV, not a replicated engine

**Chose:** OpenEBS Local PV Hostpath on the dedicated second disk, one copy per
volume, with the CloudNativePG request cut from 10Gi to 4Gi.
**Because:** CloudNativePG already replicates at the database layer - three
Postgres instances with streaming replication. A replicated storage engine
underneath stored a second copy of each, so the cluster held six copies of a
dataset whose entire encrypted backup is under 200KB. That amplification was
not theoretical: Longhorn could not schedule the third replica (3 x 10Gi x 2
against 34GB disks, minus its own 25% reserve), the state database sat at 2/3
for half an hour, and ignition reported success anyway.
**Rejected:** Longhorn, but not for being proprietary - it is CNCF and Apache
2.0, exactly like OpenEBS. Rejected for storing redundancy in the wrong layer
at this scale. Also rejected: a bigger disk, which buys the same waste with
more hardware.
**Gives up:** ReadWriteMany, storage-level snapshots, and volume survival when
a node dies. All acceptable - CNPG rebuilds a replica from the primary, and
the age-encrypted backup in object storage answers the case where more than
one node is lost. A workload that genuinely needs RWX wants OpenEBS Replicated
PV (Mayastor), which can be enabled alongside this later.

### The agent's boundary is enforced, not agreed

**Chose:** Run Claude as an unprivileged OS user with no vault access and a
GitHub identity that can push a branch and nothing else. Every limit is
enforced by a system that refuses, not by an instruction the agent is asked to
respect.
**Because:** an instruction is a request; a 403 is an answer. The agent writes
the OpenTofu that can destroy the estate, so the interesting question is not
whether it behaves but what it is still able to do when it does not - after a
bad prompt, a confused session, or a prompt injection arriving through a file
it was asked to read. Everything below survives all three, because none of them
changes what the token is permitted to do.
**Rejected:** restricting capability by removing tools. The agent needs `tofu`,
`git` and the repository to be useful at all, and an agent that cannot act is
just a worse editor. The scope that actually matters is **time**: credentials
are handed over for a session and taken back, rather than sitting in the
environment permanently at reduced power.

Three layers, each independent of the others:

| Layer       | Enforced by                                                                                                                     |
| ----------- | ------------------------------------------------------------------------------------------------------------------------------- |
| Workstation | OS user `claude` (uid 1002). Not in `sudo`, `docker`, `adm`. `/home/dev` is `0750 dev:dev`. `/tmp` is polyinstantiated per user |
| Vault       | No 1Password account configured, no `OP_SERVICE_ACCOUNT_TOKEN`                                                                  |
| Forge       | Fine-grained PAT: push on `fensberg/homelab` only, no admin, no `workflow`                                                      |

The consequence is the working model: **the agent proposes, the human
disposes.** Claude opens a pull request; `main protection` requires an
approving review it cannot give - GitHub refuses a self-approval outright -
and `require_last_push_approval` closes the case where it pushes again after
one. Nothing merges without a person.

**The asymmetry is deliberate and worth keeping.** The agent can _read_
rulesets, environments and protection rules, and cannot _write_ any of them.
That is what lets it audit the estate's own configuration - confirming
`staging`, `production` and `integration` all carry required reviewers, say -
while being unable to weaken what it just inspected. A boundary that blocked
reading too would have made the agent useless for exactly the work it is best
suited to.

**Commits are signed** with an SSH key generated on the workstation and
registered against the bot account, and `main` carries `required_signatures`.
That is provenance rather than authorisation: it does not grant the agent
anything, it makes what the agent did attributable and forgery-resistant. The
private half never leaves the workstation, and the agent cannot register keys
itself - `POST /user/ssh_signing_keys` is 403 - so a human is in that loop too.

#### Verify it rather than believing this table

The boundary is only true until somebody widens a token. Re-checking it is
cheap, and every check below is read-only except the two marked, which are
safe because they are _expected to fail_:

    # Workstation
    id; sudo -n true; docker ps; ls /home/dev
    ls /tmp-inst/; ls /tmp-inst/dev          # both must be Permission denied

    # Vault
    op whoami; op vault list; env | grep -c '^OP_'

    # Forge - all of these must 403
    gh api repos/fensberg/homelab/branches/main/protection
    gh api repos/fensberg/homelab/actions/secrets
    gh api -X PUT repos/fensberg/homelab/environments/probe          # write, must fail
    gh api -X PUT repos/fensberg/homelab/rulesets/<id> -f enforcement=disabled  # write, must fail

    # Workflow scope: commit a change under .github/workflows/ and push to a
    # throwaway branch. The push must be rejected server-side.

**Test the wall, do not infer it.** The first attempt at the `main` check used
`git push --dry-run`, which reported success - `--dry-run` skips the ref update,
so branch protection is never evaluated. Taken at face value it would have
concluded `main` was writable by the agent. The real answer came from reading
the ruleset. A privilege check that has only ever been reasoned about is not a
privilege check.

### `/tmp` was the hole in the workstation layer

**Found while writing a manual procedure around it, which is the tell.** The
Workstation row above was true and incomplete: `/home/dev` is `0750`, but `/tmp`
is `1777` and both accounts live in it. It was the one path the privilege wall
did not cover, and three things were crossing it.

The Backup phase wrote the estate's encrypted state there under a predictable
name, at the caller's umask, so every ignition run left `claude` a readable copy
of the whole estate's state - fixed in the commit this record accompanies. Agent
scratchpad directories were mutually visible: `claude` could not read
`/tmp/claude-1000` but could see that it existed, who owned it and when it was
touched. And the VS Code servers for both accounts kept their sockets there.

**Chose:** polyinstantiate `/tmp` and `/var/tmp` per user with `pam_namespace`,
so the two accounts share no filesystem path at all.
**Rejected:** telling the operator to prefer a home directory over `/tmp` when
handling secrets. That is a workaround, and a workaround has to be remembered
every time; this does not.

    # /etc/security/namespace.conf
    /tmp       /tmp-inst/       user   root
    /var/tmp   /var/tmp-inst/   user   root

    # session required pam_namespace.so             -> /etc/pam.d/sshd, /etc/pam.d/login
    # session required pam_namespace.so unmnt_remnt -> /etc/pam.d/su

**`unmnt_remnt` on `su` is load-bearing.** It unmounts the caller's instance and
mounts the target's. Without it, `su - dev` from a terminal running as `claude`
leaves dev inside _claude's_ `/tmp`, writing dev's temporary files into a
directory claude owns - strictly worse than the shared `/tmp` it replaced.

**The instance directories are `1777`, and that is correct.** `pam_namespace`
copies the polydir's own mode, and `/tmp` is sticky and world-writable by
convention. The enforcement is the parent: `/tmp-inst` is `0000 root:root`, so
nothing but root has search permission on it and the instances underneath are
reachable only through each user's own bind mount. Tightening the instances
would break software that expects a normal `/tmp` and would protect nothing.

**Verified from both sides rather than from the config.** As `claude`,
`/tmp/claude-1000` went from visible to `No such file or directory` and the
mount namespace id changed; `ls /tmp-inst/` and `ls /tmp-inst/dev` both return
`Permission denied`. As `dev`, `/tmp` contains only dev-owned entries. Same
path, two disjoint directories.

**Gotcha, and it costs an hour if it is not written down.** The VS Code server
keeps its socket in `/tmp`. A server started before this change holds a socket
in the old shared `/tmp`, so the next connection fails with
`CodeError(AsyncPipeFailed(... NotFound))` - an error that points nowhere near
its cause. Both accounts need their server killed once, and the kill only
sticks after the client has disconnected: killing a server with a client still
attached just makes the client rebuild it.

### An agent held `dev` for at least two sessions, and nothing noticed

**What happened:** connecting VS Code to the devbox with Remote-SSH as `dev`
makes `dev` the window's remote user, so the extension host and everything in
it runs as `dev`. Two artefacts showed it had happened more than once: a
`dev`-owned agent scratchpad at `/tmp/claude-1000` dated the previous
afternoon, and a second from that evening, alongside a bundled GitHub Copilot
process running headless as `dev`. None of the three layers in the table above
is violated by this - and none of them detects it either, because the
boundary describes what the `claude` account can reach, not which account an
agent happens to be running as.

**The property that actually matters is narrower than "no VS Code as `dev`".**
An editor running as `dev` is ordinary administration. An autonomous coding
agent holding `dev` is the thing the wall exists to prevent. So the fix is to
control what runs inside a `dev` window rather than to give the window up:
uninstall agent extensions from that remote, and disable the AI components
bundled into the server build itself, which cannot be uninstalled:

    # ~/.vscode-server/data/Machine/settings.json, on dev - scoped to this host only
    { "chat.disableAIFeatures": true }

**Check it the same way as everything else here.** The brackets stop the
pattern matching its own command line, which is a false positive worth
avoiding when the answer is meant to be reassuring:

    ps -u dev -o pid,cmd --no-headers | grep -Ei "[c]opilot|[c]laude"

No output is the pass. Re-run it after a VS Code update, which is exactly when
a bundled AI component can come back.

### The agent's identity was a single point of failure, and it failed

**What happened:** GitHub suspended the `claude-bot-fensberg` machine account
mid-session, without warning. Every push and every API call began returning 403. The boundary had been audited that same afternoon and held perfectly - the
failure was not that the agent could do too much, it was that a third party
revoked its ability to do anything, and no part of the design had considered
that direction.

**Chose:** replace the machine account with a GitHub App
(`fensberg-claude-bot`), authenticating with one-hour installation tokens
minted from a private key held on the workstation.
**Because:** the ToS permits machine accounts - "we do permit machine
accounts", one free machine account alongside a personal one - so the previous
setup was not against the rules. But GitHub's own documentation recommends
Apps for automation and scopes personal access tokens to "API testing or
short-lived scripts", which is exactly what a PAT driving every push was not.
An App is also structurally immune to what happened: there is no user account
to suspend.

It matches something the user had already asked for and the PAT had quietly
failed to deliver - credentials scoped by time rather than by capability. An
installation token expires in an hour by construction; the PAT expired never
and was revoked only by hand.

**Three things this cost, worth knowing before trusting any single identity:**

- **Everything the suspended account authored became invisible**, including to
  the API. Seven merged pull requests vanished from the list entirely. The
  _code_ survived because it was merged, and so did the reasoning - but only
  because this project writes long commit messages and epoch records. A pull
  request description is the ephemeral half of the record. This is the
  strongest argument yet for the convention that was already here.
- **Commit signing had to be rebuilt on a different mechanism.** An App cannot
  hold a signing key, since SSH and GPG signing keys are user-account
  resources. GitHub signs on the App's behalf instead, for commits created
  through the Git Data API - see `scripts/signedpush`. Registering the key to
  the human's account was rejected: it would make the agent's commits appear
  to be theirs.
- **An App installs on _all repositories_ by default.** The first installation
  reached eight, including a private one, until it was checked and narrowed to
  `homelab` alone. Nothing warned; the widening was silent. The rule that
  keeps recurring in this record applies to the replacement as much as to the
  thing it replaced: verify the wall, do not assume it.

**What did not change:** the read-but-not-write asymmetry. The App can read
rulesets and environments and cannot write either, so it can still audit the
estate's own protection settings without being able to weaken what it just
inspected. That property was worth deliberately preserving through the
migration rather than rediscovering afterwards.

### The self-hosted runner belongs in this epoch, not the next one

**Chose:** build the runner here, before the epoch closes.
**Because:** the goal of this epoch is "enough of a platform that later epochs
can deploy through CI instead of locally", and without a runner that sentence
is not true. `deploy-infrastructure.yml` and `integration-tests.yml` both
declare `runs-on: self-hosted`, and `tests/go/repo/selfhosted_test.go` already
enforces rules about how a pull request may reach it. All three describe a
machine that does not exist: the workflows cannot run at all, and the guard
protects nothing. Closing the epoch in that state would sign off a promise the
estate cannot keep.
**Rejected:** opening epoch 02 with it. Epoch 02 is about reusable modules, and
its first act would be to finish 01's goal - which is how an epoch boundary
stops meaning anything.

**It follows the shape everything else here follows.** OpenTofu creates only
what Flux cannot hold - the namespace and the credential - and the controller
and scale set are declared in `clusters/management/` and reconciled from git,
exactly as CloudNativePG and the state database are. The credential sits in
the account plane rather than the site plane, for the same reason the object
storage account does: it is one GitHub App for the estate, and a runner is a
site-level deployment of an estate-level identity.

**A GitHub App, not a PAT.** A PAT carries a person's identity and outlives
whoever created it; an App is scoped, independently revocable, and its
installation token is short-lived by construction. This estate already runs one
App for the agent's own pushes, and the same reasoning applies twice over to a
credential that lives inside the cluster.

### The runner build, and the order it goes in

**The identity.** One GitHub App installed on the organization, not on the
repository. Estate-scoped by construction - the organization is what bounds the
estate - and narrowed twice over: the permission is `Organization Self-hosted
runners: read & write` plus `Metadata: read`, which can add and remove runners
and nothing else, and the scale set is placed in a runner group restricted to
this repository so the App's reach does not silently widen when a second
repository joins the organization.

That scoping is the fail-closed invariant applied. A repository-scoped App
would carry `Administration: read & write`, which includes branch protection -
the mechanism that makes "the agent proposes, the human disposes" true. Same
key, same storage, same likelihood of theft; one worst case is a compromised
review requirement and the other is a runner list that needs tidying.

**The scale set keeps the name `self-hosted`.** With runner scale sets
`runs-on` matches the installation name exactly, so this is a naming decision
rather than a label decision, and the existing `runs-on: self-hosted` in
`deploy-infrastructure.yml` and `integration-tests.yml` stays untouched along
with the guard in `tests/go/repo/selfhosted_test.go`. The reasoning is that the
App is restricted to this repository, so within this repository `self-hosted`
resolves to exactly one thing and cannot be ambiguous. A second estate has its
own organization and its own App, so the word means nothing there rather than
meaning the wrong thing - which is the failure a more specific name would have
been protecting against. Verify at build time that ARC accepts `self-hosted` as
a scale set name, since it is also GitHub's implicit label for every
self-hosted runner; if it refuses, that is the moment to revisit, not before.

**What builds what.** The division of labour is the one already used for the
state database, unchanged: OpenTofu creates only the namespace and the
credential secret, rendered from the vault; the controller and the scale set
are declared in `clusters/management/` and reconciled by Flux.

1. Vault items, then the config template entries that reference them - in that
   order, because every `op://` reference must resolve.
2. `management/cluster/runner.tf`: the namespaces and the App credential.
3. `clusters/management/infrastructure/controllers/`: the controller.
4. `clusters/management/infrastructure/configs/`: the scale set.
5. Network isolation between the two namespaces - which does not exist yet,
   for the reason below.

**Controller and runners live in separate namespaces**, and the App secret is
mounted only in the controller's. A compromised job is then not in the same
namespace as the credential, which is what makes the low likelihood in the
threat model actually low rather than asserted.

### The NetworkPolicy this design asked for would not have done anything

**Corrected before it shipped.** The plan above called for a deny-by-default
`NetworkPolicy` in both runner namespaces, and that was written without
checking what enforces one here. Nothing does. This cluster runs Talos's
default CNI, Flannel - no `cni` override appears anywhere in `talos.tf` or the
machine config patches - and Flannel does not implement `NetworkPolicy` at all.
The API server accepts the object, stores it, and reports it happily. Nothing
ever evaluates it.

That is the same failure this epoch already found once, in a different place: a
guard that protects nothing, indistinguishable from a guard that works right up
until the day it matters. Shipping it would have been worse than shipping
nothing, because the record would then say the namespaces are isolated.

**So the separation that exists today is the namespace boundary and the
credential's scope, and no more than that.** The controller and the runners
hold separate copies of the same secret rather than sharing a mount, and the
App can do nothing but register and remove runners. Neither of those depends on
network enforcement. What is genuinely absent is any restriction on what a
runner pod may talk to.

**And the policy as drafted was wrong on its own terms, separately from being
inert.** It would have denied runner pods the Proxmox management network and
the vault - which is precisely what `integration-tests.yml` needs, since it
renders config from 1Password and plans against real state. A policy that
worked would have broken the workflows the runner exists to run.

**The shape that is actually right is two scale sets, not one policy.** Jobs
that touch the estate need estate access and get it; pull request lanes, when
they move here, need the opposite and should run in a second scale set with a
different label and no route to anything. That distinction is enforceable by
policy only once a CNI that implements policy is installed, which makes the CNI
the real prerequisite for moving pull request lanes - ahead of the tool cache,
and ahead of the worker node.

### Pull request lanes stay on hosted runners for now

The eight lanes in `pr-validation.yml` all run on `ubuntu-latest`, and moving
them here is wanted - a long-lived runner can keep a warm tool cache, which is
what the persistent tool-cache entry in Deferred is about. It is a separate
change from building the runner, and it has three prerequisites that are not
free.

**The fork approval setting is the one that unblocks it, and it is done.** The
organization now requires approval for all outside collaborators before any
workflow runs, across every repository. Anyone may still open a pull request;
nothing executes until a maintainer approves it. That is what makes running
untrusted code on estate hardware a decision rather than an exposure, and
combined with ephemeral per-job pods it closes the persistence attack that
makes a long-lived self-hosted runner on a public repository dangerous.

**`harden-runner` does not survive the move unchanged.** This repository's
egress posture is deny-by-default, enforced by pinning `harden-runner` to
`egress-policy: block` in every job. That works by manipulating the runner
host's networking and does not hold the same way inside a container. The
replacement is a `NetworkPolicy`, which is arguably the stronger form - the
cluster enforces it rather than a process inside the job it is policing - but
it is a migration to perform, not a property that carries over on its own.

**CI does not belong on the control plane.** Eight concurrent jobs, several
pulling large images, on the same nodes as etcd is a way to make the control
plane intermittently unwell. Runners want a dedicated worker node, which is
also where the tool-cache volume belongs.

**And "hermetic" starts doing more work.** Today the Test lane's hermeticity is
what makes a fork's pull request safe to run in full. Once it runs on estate
hardware, hermetic is a property being asserted rather than one a disposable
virtual machine was enforcing on our behalf.

### The button was a first-run tool, and nothing said so

**Found by asking what a second run would do.** After a successful ignition
Sterilize deletes both the local state file and `backend_pg.tf`, which is
correct - a workstation should hold nothing afterwards. The consequence nobody
had traced is that the next `ignite` run starts from an empty workspace. It
would plan to create every VM from scratch against an estate where they already
exist, and if it reached Migrate, `init -migrate-state -force-copy` would copy
that empty state over the real one. Only `destroy` and `sterilize` ever
reconnected to the Postgres backend.

So there was no supported way to apply a `management/` change to a running
estate. `deploy-infrastructure.yml` could not cover it either: it is
path-filtered to `environments/**` and `modules/**` deliberately, so ignition
changes never trigger it.

**Chose:** `steward converge`, a second sequence rather than a flag on the
first.
**Because:** the two runs differ in what they may assume. Ignition may assume
nothing exists; convergence may assume everything does. Expressing that as
`ConvergePhases` - `attach` in front, no `migrate`, no `hypervisor` - makes the
difference reviewable in one place instead of scattered across conditionals
inside phases that would then have to be read twice.
**Rejected:** detecting the situation automatically. "Is there state in
Postgres?" is answerable, but the two wrong answers are a duplicate estate and
an overwritten state file, and neither is worth risking to save typing a flag.

**Attach carries three refusals, and the third is the one that matters.** It
refuses when local state exists, because that is an ignition that stopped
before Migrate and this workspace already holds the authoritative copy. It uses
`init -reconfigure` rather than `-migrate-state`, because migration is the verb
that copies one state over another. And it refuses when the backend it just
attached to is **empty** - because an init against an empty backend succeeds
exactly as loudly as one against a populated backend, and applying afterwards
would build a second estate beside the first with the same names, VM ids and
addresses. Nothing inside the process can distinguish "new estate" from "lost
state", so it stops and says which two situations it cannot tell apart.

**A converge never destroys on failure.** Ignition's failure path destroys what
it built, because a half-finished ignition leaves VMs nothing is tracking and
destroying them is the safe end. A converge begins from an estate that was
already running, so answering an unreachable hypervisor or a slow Flux
reconcile by tearing down production would be precisely wrong. `-converge`
therefore implies the no-destroy path, rather than relying on anyone
remembering `-keep-on-failure`.

**This does not weaken "ignition is local-only".** Ignition still cannot run in
CI, because it builds the cluster CI runs on. Convergence has no such
circularity - the cluster is already there, holding the state - so it may run
anywhere that can reach the estate, including on the self-hosted runner inside
it. That is the remaining half of this epoch: the runner exists now, and
`deploy-infrastructure.yml` has to learn to converge `management/**` on merge,
at which point changing `control_plane_count` and merging is the whole
operation.

### Rebuilding from a total loss is a plan, not yet a capability

**The question that produced this:** if there were zero existing
infrastructure, does the estate build itself back out of R2?

**No, and two separate things are in the way.** The first is the honest floor
this epoch already records - the Hypervisor phase needs a reachable Proxmox
host with credentials in the vault, and a credential issued by a third-party
console cannot be automated into existence. So a rebuild starts with a human
installing Proxmox. The claim worth making is "install Proxmox, put its
credentials in the vault, run two commands", and it is a good claim; it is just
not "out of nothing".

The second is that `restore` puts state back and stops, by design - what to do
next is a judgement call. That leaves the actual rebuild sequence unwritten
anywhere, which is how a recovery path rots. It is now a runbook in
[`docs/state-and-secret-rotation.md`](../state-and-secret-rotation.md).

**Restore before ignition, and the reason is `talos_machine_secrets`.** A clean
ignition on fresh hardware works and produces a functioning cluster - a
different one. That resource's value _is_ the cluster's PKI, it lives in state
and nowhere else, and losing it means every kubeconfig, machine certificate and
prior backup belongs to a cluster that no longer exists. Restoring first adopts
the identity rather than minting a new one. The bucket and the tailnet key make
the same argument more mildly.

**None of it has been run.** The sequence follows from what the code does and
that is all it does. Ignition has also never run against a factory-fresh
Proxmox host - the same gap seen from the other side, already in Deferred. The
drill is epoch 01 scope rather than a later nicety, because the estate is
disposable exactly once and that property expires the moment something worth
keeping is on it.

## Acceptance test: change 3 to 5 and watch it land

The epoch is signed off when this works, end to end, with no manual step in
the middle:

> Change `control_plane_count` from `3` to `5` in the config. **Merge it.**
> Two more machines exist, joined to the cluster - with nobody having run
> anything.

The wording used to say "run the button", and that was the weaker test. A
human running a command after a merge is the step this tier exists to remove;
if it survives, the estate is still operated by hand and only its
configuration is in git. The test is what it is because passing it means
`management/` is genuinely reconciled rather than merely codified.

It is the right test because it exercises the whole chain in one edit rather
than any single layer. The count drives addressing (`10.10.10.100-104`),
naming (`sheridan-cp-01..05`), VM ids (`1000-1004`), placement, the Talos
machine configuration applied to each new node, and the wait that proves each
one answered. If any of those is hardcoded, hand-maintained, or quietly
assumes three, this fails and names the place.

### Why 5 and not 4

Worth stating, because this epoch already records that
`control_plane_count` is a provisioning input and **not** an autoscaler, and
that etcd quorum is fixed at creation. Those are not in conflict with this
test.

Going 3 -> 5 is odd to odd: quorum moves from 2-of-3 to 3-of-5 and the cluster
gains fault tolerance. Going 3 -> 4 adds a member without adding a tiebreaker,
which is the thing that must never happen. The rule the earlier decision is
protecting is "never scale etcd automatically, and never to an even number" -
not "never change the count deliberately".

### What to check before running it

- **Hypervisor capacity.** Five control-plane VMs at 4 cores and 4GiB each is
  20 cores and 20GiB, alongside the devbox. Confirm the host has it, because
  the failure mode otherwise is two VMs that create and never boot.
- **Datastore capacity.** Each node carries a 64GiB OS disk and a 32GiB
  storage disk, so five nodes is ~480GiB on `local-zfs` before the template
  and the devbox.
- **Placement stays put.** With one hypervisor the round-robin is a no-op -
  every node lands on it either way - so the re-deal hazard recorded in
  [`02-abstraction.md`](02-abstraction.md) does not bite here. It would the
  moment a second hypervisor exists, which is exactly why that entry says
  adding a hypervisor to a running site has no safe meaning yet.

## Outcome

**Not yet signed off.** The acceptance test above is the gate, and it has not
run: `control_plane_count` moved from 3 to 5 in its own change, and the button
has to carry that end to end with no manual step before this section can claim
anything. What follows is what exists, written now while it is fresh, so that
signing off is a matter of confirming rather than reconstructing.

**Built.** A phased Go entrypoint (`scripts/steward`) that renders secrets from
1Password, prepares a Proxmox host with an idempotent Ansible playbook,
provisions a Talos control plane, bootstraps Flux, migrates its own state into
cluster Postgres, backs that state up encrypted to object storage, and wipes
the workstation on the way out. Storage is OpenEBS Local PV; the state database
is CloudNativePG, reconciled by Flux, streaming to object storage.

**Proven, rather than assumed.** The estate came up end to end on 2026-08-30.
The recovery path was drilled the same night rather than inferred: the R2
object was fetched, decrypted with the identity from the vault, and confirmed
to be state describing a real estate (schema version 4, serial 1, nineteen
resources). The privilege boundary was re-verified from the unprivileged side,
and the `/tmp` hole found in the process was closed rather than documented
around.

**The honest limit.** Ignition has never run against a factory-fresh Proxmox
host. Every run so far met a hypervisor that already carried the SDN, the
tailnet membership, the API token and the disk-import account from an earlier
run. The playbook is idempotent so those runs pass, but the epoch's promise is
"install Proxmox, run the button, get a cluster" and what has actually been
demonstrated is "start from an almost-fresh Proxmox". That gap closes when a
second hypervisor exists, and it should not be quietly upgraded at sign-off.

**Deliberately left for later epochs.** `modules/` and `environments/` - the
abstraction and workload tiers - are epochs 02 and 03. Everything else this
epoch chose not to do is in Deferred below, each with the trigger that should
bring it back.

## Deferred

- **`insecure = true` on the Proxmox provider** — trigger: a trusted cert.
- **QEMU guest agent** is off deliberately; Talos will not report ready
  without the extension in the Factory schematic.
- **The total-loss drill: restore, then ignite, on a wiped host.** The
  sequence is written up in `docs/state-and-secret-rotation.md` and has never
  been executed. It is the other half of the backup drill already recorded
  here: that one proved the object decrypts, this one proves it rebuilds.
  Trigger: **now, while the estate is still disposable** - this is epoch 01
  scope, not a later nicety.
- **A CNI that implements `NetworkPolicy`.** Talos's default is Flannel,
  which does not, so a policy object here is accepted and never evaluated.
  This is the prerequisite for isolating runner workloads from the estate,
  and therefore for moving pull request lanes onto the self-hosted runner at
  all. Cilium or Calico; the choice is its own decision. Trigger: before any
  workload that should not reach the hypervisor runs on this cluster.
- **Self-hosted Renovate**, to replace or complement Dependabot for the
  ecosystems it can't cover at all: a Flux `HelmRelease`'s chart version
  (Longhorn's is a manual bump today) and a digest-pinned container image
  embedded in a workflow's `container:` block (Semgrep's is too). Trigger:
  once the cluster is up and running, so it has somewhere to actually run -
  **fired**: site0 is up, so this is now actionable rather than blocked.
- **A persistent tool-cache on a self-hosted runner**, so pinned binaries
  this repo already downloads-and-verifies fresh every run (tofu,
  kubeconform, the trufflehog CLI) don't repeat that fetch on every job -
  a long-lived runner can keep them warm across runs instead. The
  verification step doesn't go away, it just stops repeating on every run
  for a version that hasn't changed. Actions Runner Controller with a
  persistent volume backing the runner's tool-cache directory is the
  natural mechanism, since `deploy-infrastructure.yml` already targets
  `self-hosted`. Trigger: once self-hosted runners exist.

- **Ignition has never been run against a factory-fresh Proxmox host.** Every
  run so far has been against a hypervisor that already had the SDN, the
  tailnet membership, the API token and the disk-import SSH account from an
  earlier run. The playbook is idempotent, so those runs pass - but "install
  Proxmox, run the button, get a cluster" is the epoch's actual promise and it
  is currently unproven. What has been proven is "start from an almost-fresh
  Proxmox", which is a weaker claim and the honest one to make until then.
  Trigger: buying a second hypervisor. That is the first machine that can be
  wiped without taking the estate down with it, and the first genuine test of
  the client-onboarding story in
  [`02-abstraction.md`](02-abstraction.md).

## Deferred (Ansible)

- **`apt_repository` is deprecated** in favour of `deb822_repository` and is
  removed in ansible-core 2.25. Migrating means supplying `signed_by` keyrings
  explicitly for both the Proxmox and Tailscale repositories, which is not a
  change worth making untested against a live hypervisor. The warning is muted
  in `ansible.cfg` so it does not bury real output. Trigger: an ansible-core
  upgrade approaching 2.25, or the next time that playbook is exercised
  against a disposable host.
- **The Hypervisor phase authenticates as `root@pam` over SSH, for every run,
  not just the first one.** Necessarily true on a factory-fresh Proxmox
  install - there is no non-root account to use yet - but every re-run after
  that still goes over root SSH too. A harder setup would have the first run
  create a dedicated non-root admin user with sudo, install this SSH key for
  that user, and have every task after that - including the rest of that
  same first run - use it via `become: true`, ideally disabling
  `PermitRootLogin` once it exists. Deferred deliberately: get ignition
  working end to end first, harden the bootstrap identity after. Trigger:
  once the MVP cluster is up and the project moves from MVP to V1.

## Gotchas

- **`tailscale up` blocks forever if the control plane is unreachable.** It
  waits for the coordination server to confirm the login and prints nothing
  while it does, so a wedged or disconnected `tailscaled` presents as a hung
  playbook rather than an error. Every invocation carries `--timeout`.
- **`--force-reauth` logs the node out before logging it back in.** A timeout
  firing in between leaves the host logged out and worse off than before the
  run - which is why the timeout is 180s rather than something tight. The
  state is recoverable: a logged-out host takes the plain login path on the
  next attempt, with no `--force-reauth`. Originally that "next attempt"
  meant a human noticing the failure and re-running the whole start button -
  discovered too manual on the first real hardware run, so the login task now
  retries itself in place (`until`/`retries: 2`/`delay: 15`) rather than
  surfacing this as something the operator has to catch and fix by hand. See
  "The Tailscale login retries itself" below.
- **The overlay network is load-bearing for ignition only because the
  workstation is remote.** The Verify phase reaches the SDN gateway across the
  tailnet, so a Tailscale outage blocks provisioning entirely. Running the
  button from a machine on the hypervisor's own LAN removes that dependency
  and leaves the overlay network for remote access, which is what it is
  actually for.
- **Enabling IPv6 forwarding silently destroys IPv6 connectivity on a SLAAC
  host.** The kernel stops honouring router advertisements unless `accept_ra`
  is also set to 2, so the host loses its global address and default route the
  moment forwarding is switched on. This playbook enabled it for a while and
  broke exactly that.

  The symptom does not look like IPv6. `tailscaled` keeps selecting IPv6 DERP
  relays and failing with `network is unreachable`, while every IPv4 test
  passes - control plane reachable, relays reachable, MTU intact, firewall
  clear. It reads as a wedged daemon, and restarting it appears to help for a
  moment because a fresh netcheck retries IPv4 first.

  When `tailscaled` cannot reach the coordination server, check
  `ip -6 route show default` before anything else.

- **A targeted apply only writes outputs that depend on the targeted
  resource.** The phased design applies with `-target`, so an output built from
  a bare local depends on nothing, is never written to state, and
  `tofu output` reports it as not found - even though `tofu validate` and the
  plan are perfectly happy. Any output the start button reads between phases
  must derive from a resource that phase actually creates.

- **"tailscaled is running" does not mean "logged in the way this needs".** A
  host logged in manually is user-owned and carries no tags, so `autoApprovers`

  - which is keyed on the router tag - never applies to it and every route it
    advertises waits for a human. The playbook checks for the tag, not for the
    service, and re-authenticates with `--force-reauth` when it is absent;
    converting a user-owned node to a tagged one cannot be done any other way.

- **Applying the SDN config does not bring the bridge up.**
  `pvesh set /cluster/sdn` writes the interface definition and stops, leaving
  the vnet DOWN with its gateway address assigned - existing, addressed, and
  reachable from nowhere. The web UI finishes the job with `ifreload -a`,
  which reloads every interface on a live hypervisor and can drop the
  management link, so the playbook brings up only the one interface instead.

- **Proxmox privilege names change between major versions.** `VM.Monitor`
  existed in PVE 8 and does not in PVE 9. `pveum` rejects the whole role with a
  bare "400 Parameter verification failed" naming only the first offender, so
  the playbook validates the requested list against the built-in Administrator
  role - which holds every privilege the running version defines - and names
  all of them at once.

- **Ansible reaches the hypervisor over SSH from inside WSL**, not from
  Windows, so it is WSL's key and WSL's `known_hosts` that matter. A key
  installed for the Windows user does nothing here.
- **Host keys are accepted on first use, not blindly.**
  `StrictHostKeyChecking=accept-new` trusts a host the first time and pins it,
  but still refuses one whose key has changed. `no` would silently accept a
  substituted host forever, and the default fails outright because Ansible
  runs `ssh` without a TTY to answer the prompt.

- **Renaming a site renames its VMs.** Everything nameable derives from
  `sites.<key>.name`, so changing it in the vault makes OpenTofu see different
  VMs and destroy and recreate them. Rename deliberately, not casually.
- **The WSL command is written to a file, not passed as `bash -lc`.** WSL
  inherits the Windows PATH, which contains `Program Files (x86)`, and an
  unquoted assignment of it is a `bash` syntax error at the parenthesis. A
  script file sidesteps every layer of quoting between PowerShell, wsl.exe
  and `bash`.

- **Tailscale auth key descriptions reject punctuation.** The API returns
  "description had invalid characters (400)" for parentheses; the field is
  also capped at 50 characters. Letters, digits and spaces only.

- **The age private key is never referenced by the automation, and must not
  be.** The config contract carries only `state_backup.recipient`, the public
  half, so the start button can write backups and cannot read them. Putting the
  identity in `management.tpl.json` would render it to disk on every run and
  hand every historical backup to anyone who compromised the workstation. It
  lives in 1Password at `op://homelab/state_backup/identity` and is fetched by
  a human, by hand, only when restoring. This is no longer a matter of
  remembering: `tests/go/repo/breakglass_test.go` fails if any `.tf` file or
  the config template so much as names it.
- **The keypair is one per estate, not one per site.** It sits at the top level
  of the config, outside `sites`, because the recipient is a public key —
  sharing it across sites costs nothing, while a key per site would multiply
  the number of private halves a restore has to find. The per-site database
  password is the opposite case and stays inside the site.
- **Rotating the age key orphans every existing backup.** Backups are
  encrypted to a recipient, so a new key pair cannot read anything written
  under the old one. Rotate before real backups exist, or decrypt and
  re-encrypt the archive deliberately.

- **The tailnet policy is not managed by this code.** If ignition hangs on an
  unreachable node, confirm `docs/tailnet-setup.md` has actually been applied
  to the tailnet you are deploying into. A missing `autoApprovers` entry looks
  identical to a broken network.
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
- **`git push --dry-run` does not evaluate branch protection.** It skips the
  ref update, so a push that would be rejected reports success. It is the
  wrong tool for "can I write here"; read the ruleset instead. See "The
  agent's boundary is enforced, not agreed".
- **A commit made in the GitHub web editor can be unfixable.** The editor does
  not wrap a body, so `body-max-line-length` rejects it, and it does not
  prepend a Conventional Commits type, so `type-empty` rejects a plain subject
  like "Add workflow to monitor sensitive paths". Either failure is permanent:
  `non_fast_forward` has no bypass actors, so nobody - the repository admin
  included - can amend the commit afterwards. The only exit is a new branch,
  which cost two pull requests to learn twice. Write the subject as
  `type: summary` and wrap the body at 72 before committing from a browser, or
  commit from a terminal where the `commit-msg` hook catches it first.
- **`non_fast_forward` applies to feature branches too, not just `main`.** So
  commits on an open pull request cannot be amended or rebased once pushed -
  no force-push is possible for anybody, agent or human. Fixing a bad commit
  message means a new commit, or closing the branch and starting over.
- **With `required_signatures` on `main`, squash-merge a branch holding
  unsigned commits.** GitHub creates and signs the squash commit with its own
  key, so it satisfies the rule; a **rebase** merge replays the original
  unsigned commits and is rejected. This bites any branch that predates
  signing being switched on, and `non_fast_forward` above means those commits
  cannot be retroactively signed.
- **Merging `main` into a PR branch does not dismiss an approval**, even with
  `dismiss_stale_reviews_on_push` enabled. GitHub only dismisses on a
  _reviewable_ push - one that changes the contribution - and carries the
  approval forward onto the new merge commit, which is visible as the review's
  `commit_id` changing to a commit that did not exist when it was given. This
  is intended behaviour and the required status checks re-run on the merge
  commit, which is the compensating control; what it does not re-review is a
  semantic conflict that merges cleanly.
- **A missing GitHub environment is created on first use with no protection
  rules**, silently. Naming `environment:` in a workflow therefore proves
  nothing about whether a gate exists -
  `tests/go/repo/selfhosted_test.go` can only check the file, and says so in
  its failure message. Confirm the environment in Settings as well.
- **`op` has no existence check that does not also return the value.** Any
  "does this reference resolve" test necessarily reads the secret. The
  handling is to make the value unreachable rather than to be careful with it:
  `onepassword.Probe` returns a status and never the value, so
  `steward check-vault` cannot print one. See "The agent's boundary is
  enforced, not agreed" for the same reasoning applied to credentials.
- **The agent cannot run `task lint` at all**, because Super-Linter is a
  Docker image and the `claude` user is deliberately not in the `docker`
  group. `fix`, `validate` and `test` all run locally; **Analyze is the one
  gate that only ever fails in CI** for an agent-authored change. That is a
  cost of the boundary rather than a defect in it - the alternative is docker
  group membership, which is root on the host - but it means a pull request
  written by the agent can be locally clean and still fail Analyze. It has:
  MD046 below. Anyone reviewing agent work should expect that lane to be the
  one that catches things, and not read a green local run as a green CI run.
- **markdownlint's MD046 is `consistent`, so the first code block in a file
  fixes the style for the whole file.** `01-ignition.md` has had an indented
  block since the Windows route command, which makes a fenced block added
  anywhere later in it an error - while `tests/README.md`, fenced throughout,
  is equally correct. The rule is per-file, not per-repository, so match the
  file you are editing rather than the last one you looked at.
