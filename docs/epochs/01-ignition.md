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
program (`scripts/ignite`) plus a plain bash bootstrap
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

The bootstrap script stayed bash on purpose: `scripts/ignite` needs Go to
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
`./scripts/ignite/ignite` directly. Every other ignite-invoking task
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
