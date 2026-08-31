# Epoch 02 — Abstraction

- **Tier / path:** `modules/`
- **Branch:** `epoch/02-abstraction`
- **PR:** —
- **Status:** Not started

## Goal

Write the reusable pieces once. Turn the one-off resources proven in epoch 01
into parameterized modules that staging and production can both consume
without copy-paste.

## Scope

In scope (per `README.md`):

- `modules/infrastructure/` — OpenTofu modules with `main.tf` / `variables.tf`.
- `modules/applications/` — Kubernetes bases for Flux/Kustomize overlays.

Explicitly out of scope:

- Per-environment values and overlays — epoch 03. A module that knows it is
  "staging" is not a module.
- Changes to `management/` — epoch 01.

## Known driver: multi-site deployment

The management root is currently hardcoded to one site. `base_cidr`,
`node_count` and the derived node addresses are locals in `variables.tf`, and
`organization.name` becomes the Talos cluster name. Deploying a second site
from the same code would advertise a colliding subnet onto the tailnet and
name its cluster identically.

Epoch 01 has since parameterised this: `sites[]` in the config is an array,
and the index drives addressing, naming, placement and VM IDs. What remains
for this epoch is turning the management root into a reusable module, so a
site is an instantiation rather than a `TF_VAR_site` switch.

### The unit of addressing is the site, not the hypervisor

A subnet per hypervisor node is the wrong split. Nodes in one Proxmox cluster
must share a subnet, because a single Talos cluster spanning them needs its
members on one network. Give each _site_ a /16 and subnet within it:

| Range                | Holds                                       |
| -------------------- | ------------------------------------------- |
| `10.<site>.0.0/24`   | hypervisor and infrastructure               |
| `10.<site>.10.0/24`  | Talos cluster nodes                         |
| `10.<site>.20.0/24`  | load-balancer pool for workloads            |
| `10.<site>.30.0/24`+ | per-tenant, if tenants get their own ranges |

Site 1 is `10.10.0.0/16`, site 2 `10.20.0.0/16`, and so on. Each site's subnet
router advertises its own /16, so there is one route per site and room to grow
inside it without touching the tailnet policy again.

Stay below `10.96.0.0`. Kubernetes defaults put services at `10.96.0.0/12` and
pods at `10.244.0.0/16`; those are cluster-internal and not routed over the
overlay network, but sharing the range invites confusion when debugging.

### Why an org has two sites at all

Worth writing down, because "why not one cluster" gets asked every time and the
technical answer alone does not settle it.

**The cluster is not the unit of redundancy. The data is.**

Most multi-site is not duplication. A site is where work physically happens,
and each site's cluster serves that building - local line-of-business systems,
file services, cameras, sensors. Those clusters are not copies of each other.
Two consequences follow, and both favour independent clusters:

- **Site survivability.** If a site loses its uplink, its local services keep
  running. A stretched cluster loses quorum and goes down at _both_ ends.
  Independent clusters are more available for local work, not less.
- **Blast radius.** A bad upgrade or a corrupted etcd stops at one building.

When a second site genuinely is a standby, the replication happens below
Kubernetes: Postgres streaming replication, object storage replication, DNS or
a global load balancer to move traffic. The clusters stay independent and
disposable; the data layer is what tolerates WAN latency, and etcd is not.

The operational cost does not multiply, which is the other half of the answer.
A workload is defined once in git and Flux reconciles it to every cluster, so
ten clusters cost roughly what one costs to run. The duplication is hardware,
not effort - and that is precisely what the site array and this tier exist to
make true.

Finally, the honest case against: if a second site has no local workload and no
disaster-recovery requirement, do not build one. One site plus the object
storage backups is a complete answer, and cheaper. Multi-site because it sounds
robust is how people end up operating two of something they needed one of.

### Scaling: what autoscales, and what cannot

`control_plane_count` is a provisioning input, not an autoscaler, and it must
not become one. etcd quorum is fixed at cluster creation: adding a member
changes the arithmetic mid-flight, and an even count adds a member without
adding a tiebreaker. Pick 3, or 5 for a large site, and leave it.

Real autoscaling is four separate mechanisms, and they do not all apply here:

| Mechanism          | Scales                           | Works on this platform?                      |
| ------------------ | -------------------------------- | -------------------------------------------- |
| HPA                | Pod replicas, on CPU/memory      | Yes, once metrics-server is installed        |
| KEDA               | Pod replicas, on external events | Yes - queue depth, cron, custom metrics      |
| VPA                | A pod's requests and limits      | Yes, but it fights HPA on the same metric    |
| Cluster Autoscaler | Node count                       | **Not on bare metal without more machinery** |

Cluster Autoscaler asks an infrastructure API for another machine. On a cloud
provider that API exists. On a NUC it does not, so CA has nothing to call.

The path that does work is **Cluster API with the Proxmox infrastructure
provider**: CAPI turns "I need another worker" into a Proxmox VM creation, and
CA drives CAPI. That is the genuine answer and it is worth doing for the
learning alone, since CAPI is how large fleets are actually managed.

Two constraints to be honest about before building any of it.

**There is a hard ceiling.** CA can create VMs until the hypervisors are full
and then it stops. It cannot buy a NUC. What autoscaling buys on bare metal is
better packing and faster response to load, not elasticity. Capacity planning
does not go away; it moves from "how many pods" to "how many boxes".

**There is a prerequisite that does not exist yet.** The cluster currently sets
`allowSchedulingOnControlPlanes = true`, so every workload runs on the control
plane and there is no worker pool to scale. A separate worker machine set is
the first step, and it belongs in this epoch. Autoscaling anything before that
would be scaling the etcd members, which is the one thing that must not scale.

Order of work: worker pool, then metrics-server, then HPA and KEDA for
workloads in epoch 03, then CAPI and CA only if node-level elasticity is
genuinely needed within the rack.

### One cluster per site, not one cluster across sites

A single Talos cluster with three nodes at each of two sites is a stretched
cluster, and it should be avoided. etcd raft is latency-sensitive - members
want single-digit millisecond round trips, and an overlay-network link between
sites will not deliver that. A partition between sites also leaves six members
with no tiebreaker.

The management-tier and workload-tier split this repo already describes is the
right shape: one cluster per site, all reconciled from one git repository by
Flux. Scale by adding clusters, not by stretching one.

### Bootstrapping a site, not a node

The unit a human should have to think about is the **site**. The target
experience, stated plainly so the design can be measured against it:

> A client buys a server, racks it, installs Proxmox, and enters that node's
> credentials into 1Password. Then it just works.

Nothing in that sentence mentions this repository, and that is the point. The
person adding capacity should not be opening a pull request.

**What stands in the way is the direction the config points.** Today
`config/management.tpl.json` enumerates every site and every node explicitly,
and each entry is a set of `op://` references. Adding a node is therefore two
steps in two systems: create the vault fields, _then_ edit the template and
merge it. Epoch 01's own notes are explicit that the second step cannot be
avoided as things stand - `op inject` substitutes into a fixed file and cannot
loop over `sites[].hypervisor.nodes`, which is exactly why the Ansible
inventory is generated in Go rather than templated.

So the template is a declaration that _references_ the vault. The requirement
above inverts that: the vault becomes the source of truth, and the config is
**discovered** from it rather than declared alongside it.

That is buildable with tools already in use. `op item list --vault homelab
--format=json` enumerates the items; `op item get <site> --format=json` returns
its sections and fields, which is precisely the shape `hypervisor-prep.yml`
already reads and writes. Ignite would build the rendered config in memory
instead of injecting a template, and a new node would be discovered the moment
its section exists.

**Three things this trades away, all worth stating before choosing it:**

1. **Git stops showing the shape of the estate.** Epoch 01 deliberately made
   the template reveal the topology - how many sites, how many nodes - while
   revealing nothing about what or where they are. Discovery moves that
   knowledge entirely into the vault, and a reviewer can no longer see from a
   diff that a site gained a hypervisor. A redacted topology summary emitted by
   ignite, or committed as a generated artefact, would recover most of it.
2. **The invariants become load-bearing in a way they are not today.** A
   hand-edited template gets human review; a vault-authored config does not.
   `registry.tf` and `config.ResolveSiteNetwork` would become the only thing
   standing between a mistyped octet and a colliding `/16`. The config-contract
   corpus in `management/cluster/tests/fixtures` is what makes that acceptable,
   and it would need to grow rather than stay still.
3. **Octet assignment has to live somewhere.** It is declared, not derived, on
   purpose - so that retiring a site leaves a gap rather than renumbering its
   neighbours. Under discovery it belongs in the vault item as a field the
   client fills in, with uniqueness asserted across every discovered site
   rather than across the ones a template happened to list.

**What this means for the modules in this epoch.** A site module instantiated
once per discovered site is the shape that satisfies the requirement; a
`TF_VAR_site` switch over a single root is not, because it still needs someone
to add the site to a file. "Bootstrap a site" is therefore the acceptance test
for this tier: if adding a site still requires a commit, the abstraction is not
finished.

### Adding a hypervisor currently re-deals the control plane

The scenario above - "a client buys a server, racks it, installs Proxmox" - is
the one that breaks first, and it breaks silently.

`vm_placement` deals control-plane VMs round-robin across whatever hypervisors
a site has:

```hcl
vm_placement = [
  for i in range(local.node_count) :
  local.hypervisors[i % length(local.hypervisors)].hostname
]
```

That is a re-deal, not an append. With one hypervisor all three land on it;
add a second and the arithmetic reassigns the middle one:

| Hypervisors | cp-01 | cp-02   | cp-03 |
| ----------- | ----- | ------- | ----- |
| 1           | hv0   | hv0     | hv0   |
| 2           | hv0   | **hv1** | hv0   |

`node_name` on `proxmox_virtual_environment_vm` cannot be changed in place, so
OpenTofu resolves that as **destroy and recreate `cp-02`** - a running etcd
member - and nothing in the flow calls `talosctl etcd remove-member` first. The
likely outcome is a stale member in the etcd member list and a rebuilt node
that cannot rejoin, on a cluster that was healthy until someone added capacity.

Quorum survives the moment itself (two of three), which is what makes this
dangerous: the apply looks like it worked.

**The rule this points to: new hardware must add capacity, never re-place
existing control-plane members.** Two things follow, and they line up with the
worker-pool prerequisite already noted above:

1. **Control-plane placement must be sticky.** Once a control-plane node is
   placed it stays placed, whatever the hypervisor list does afterwards.
   Deriving placement from a modulo over a growing list cannot express that;
   the placement has to be recorded rather than recomputed - a field on the
   site, or an explicit map, so it is reviewable and stable.
2. **Growth belongs to the worker pool.** etcd is fixed at three (or five) by
   design; a new server should be absorbed by workers, which are exactly the
   thing that can move without a quorum question. Until a worker pool exists,
   "add a hypervisor" has no safe meaning for an existing site.

Until both are true, adding a hypervisor to a **running** site is a manual,
supervised operation and should be documented as one. Adding a hypervisor to a
site that does not exist yet is fine, which is the ordinary case for onboarding
a new client and the one the acceptance test above describes.

### How portable is the overlay network, really

The invariant in `CLAUDE.md` said naming things by function keeps a vendor swap
"to a single `.tf` file". Measured against the actual estate, that is true of
the OpenTofu layer and misleading about everything else:

| Where                          | Tailscale-specific                                                  |
| ------------------------------ | ------------------------------------------------------------------- |
| `overlay-network.tf`           | 60 lines (all of it)                                                |
| `versions.tf` provider block   | 2 references                                                        |
| `registry.tf` vendor assertion | 1 reference                                                         |
| `config/management.tpl.json`   | 5 fields, Tailscale-shaped (`domain`, `client_id`, `client_secret`) |
| **`hypervisor-prep.yml`**      | **~50 references**                                                  |

The OpenTofu side genuinely is a small swap. The playbook is not: signing key,
apt repository, install, `tailscaled` service, login-state detection,
`tailscale up --force-reauth`, route advertisement and the retry logic around
all of it. That is where an overlay migration would actually be spent, and the
config's field names would have to change shape too - a raw WireGuard mesh has
no notion of an OAuth client or a tailnet domain.

**What each option would actually cost:**

- **Headscale** - the FOSS coordination server that speaks the Tailscale
  protocol. The client, the tags, the `autoApprovers` concept and every
  playbook task survive; only the login server changes. By far the smallest
  migration and the one that preserves the most, which makes it the default
  answer if the motivation is "self-hosted and FOSS".
- **NetBird / Nebula** - different agents and different APIs, so the playbook
  is rewritten and the config fields change shape. A real port.
- **Raw WireGuard** - no coordination server at all, which means rebuilding
  NAT traversal, peer distribution, route advertisement and ACLs by hand.
  Those are the things being paid for today, and a homelab is exactly the
  scale at which hand-managing peers looks cheap right up until a node moves.

**The constraint that applies to all three:** the overlay is used to _reach_
the hypervisor during ignition, so a self-hosted control server cannot live
inside the estate it bootstraps. That is the same circular dependency as the
state database and the secrets manager, and it has the same shape of answer -
the control plane sits outside, or the first hop is something else entirely.

### Design constraint: no provider may depend on a resource in its own root

This is a constraint on how the modules are carved, and it is cheaper to honour
now than to retrofit.

`management/cluster/versions.tf` configures the `kubernetes` provider from
`talos_cluster_kubeconfig.this` - a resource created in the same root. That is
a documented anti-pattern, and epoch 01 has been paying for it without saying
so out loud:

- Provider configuration is evaluated for the **whole root**, so every provider
  must be resolvable for any operation. `-target` narrows that, which is why
  ignite's applies are a hand-sequenced chain rather than one apply.
- `tofu import` has no `-target`, so it can never narrow. Any import before the
  cluster exists fails on the kubernetes provider rather than on the resource
  being imported - confirmed by running both against the real estate, where a
  `-target`ed plan of a Proxmox resource succeeds and an import of that same
  resource does not.
- The cost is confined to the bootstrap window, because once the resource is in
  state the provider resolves from it. But ignition _is_ the bootstrap window,
  and with N sites it is a window somebody is inside most of the time.

**The seam already exists**, cleanly, along current file boundaries - not one
resource straddles it:

| Layer            | Files                                                               | Providers                             |
| ---------------- | ------------------------------------------------------------------- | ------------------------------------- |
| `infrastructure` | `compute.tf`, `talos.tf`, `overlay-network.tf`, `object-storage.tf` | proxmox, talos, tailscale, cloudflare |
| `platform`       | `database.tf`, `gitops.tf`                                          | kubernetes                            |

Split there and the platform layer configures its provider from the
infrastructure layer's **output** - a value already applied, therefore known at
plan time. The ordering moves out of `cluster.go` and into the structure,
imports work in either layer, and `-target` goes back to being what OpenTofu
says it is: for exceptional situations, not routine use.

The cost is two applies and a handoff between them. Ignite already writes the
kubeconfig to a file for the Flux bootstrap, so the mechanism is not new.

**If `modules/` is carved out of the current monolithic root without this
split, the coupling is baked into the module boundaries and every future site
inherits it.**

## Known driver: the cluster reaches the hypervisor over a flat network

A converge that changes the number of machines has to call the hypervisor's
API, and the self-hosted runner that performs it is a pod inside the cluster
those machines host. Today that pod reaches the hypervisor by plain IP across a
flat path, because a firewall rule permits the node subnet to reach the
management address on one port.

That rule is deliberate and recorded rather than accidental, but it is the
weaker answer, and it is worth saying exactly why so the replacement is not
argued from taste.

**The grant cannot be narrowed below the subnet.** The cluster runs Flannel,
which does not enforce NetworkPolicy, so "only the runner pod may reach the
hypervisor" is not expressible. Any pod on those nodes can reach the API. The
credential remains the real control and it lives in the vault, but the network
layer contributes nothing, and a control that cannot be narrowed is one that
only ever widens.

**It contradicts the model the estate already has.** `overlay_network` is a
first-class concern with its own provider abstraction, its own vendor
attestation, and a hypervisor that already joins the tailnet and advertises
routes. Reaching that same hypervisor by flat IP means the estate has two paths
to the same host, one of them governed by an ACL and one by a firewall line.
Two paths is one more than can be reasoned about.

**It does not survive the second site.** This epoch exists to make a second site
possible. A flat rule is per-site plumbing that has to be recreated, by hand or
by Ansible, for every hypervisor added - and the moment two sites exist, "the
node subnet" stops being a meaningful source, because each site has its own.

### The replacement

Reach the hypervisor over the overlay, addressed by its tailnet name, with
access governed by tailnet ACLs rather than by a firewall rule. That scopes by
identity instead of by network, which is the granularity Flannel cannot give,
and it collapses the two paths back into one.

It also removes an asymmetry that exists today: the workstation reaches the
hypervisor over the LAN, and the cluster reaches it over a firewall exception.
If the canonical endpoint is the tailnet address, both use the same value and
the same policy, and operating the estate from somewhere other than the LAN
stops being a special case.

The open question is how the cluster joins the tailnet, and it is a real
decision rather than a detail:

- **The Tailscale Kubernetes operator**, which can expose a tailnet target as an
  in-cluster Service. Least invasive, but the endpoint then differs depending on
  where the code runs, which reintroduces two values for one host.
- **Talos node extensions**, putting the nodes themselves on the tailnet so pods
  egress through them. One endpoint everywhere, at the cost of every node
  holding tailnet membership.

Neither is obviously right, which is precisely why the flat rule was taken as
the interim rather than one of these being chosen in a hurry to unblock a test.

## Known driver: the config fixture corpus does not scale

Moving two fields from the site plane to the fleet plane - `account_id` and
`admin_token`, one small, correct change - touched **23 files**. Nine of them
were test fixtures, and most differed from `valid.json` by two lines out of
fifty-one:

| Fixture                         | Lines differing from `valid.json`   |
| ------------------------------- | ----------------------------------- |
| `aws-shaped-key.json`           | 2 of 51                             |
| `vendor-mismatch.json`          | 2 of 51                             |
| `control-plane-count-zero.json` | 2 of 51                             |
| `missing-vault-provider.json`   | 2 of 51                             |
| `octet-out-of-range.json`       | 4 of 51                             |
| `unimplemented-vendor.json`     | 4 of 51                             |
| `no-hypervisor-nodes.json`      | 7 of 51                             |
| `duplicate-octet.json`          | 34 of 51 (adds a whole second site) |

Every fixture restates an entire config in order to vary one field. Adding a
field to the config means editing ten files that have nothing to say about it,
and adding a test case means copying fifty lines to change two. That is a
corpus that will be wrong before it is complete: the failure mode is not a
loud one, it is a fixture nobody updated that keeps passing while asserting
the wrong shape.

### Which duplication here is deliberate, and which is not

Worth separating, because the answer is not "remove all of it".

**Deliberate, and it stays.** `registry.tf` and `config.go` implement the same
invariants twice so a bad config is refused whether it arrives through the
start button or a bare `tofu plan`. `tests/go/harness` declares its own reader
for the same reason its comment gives - a test that parsed the file with the
program's own code would agree with that program about a misreading. Both are
defence in depth, both cost an edit when the shape changes, and both are worth
the cost.

**Not deliberate.** Nine near-identical JSON documents are not a design, they
are what happened. Nothing is being checked twice by `aws-shaped-key.json`
carrying a full `hypervisor` block; it carries one because it was copied.

### The pattern to borrow

Gherkin and Playwright both solved this, and in the same direction: **express
the variation, not the whole.**

- A Gherkin `Scenario Outline` writes the scenario once and puts only the
  varying values in an `Examples` table. The prose is not repeated per case.
- Playwright's fixtures compose - `test.extend` layers a narrow override onto
  a base fixture, so a test declares only what makes it different, and a
  change to the base reaches every test that builds on it.

Applied here, `aws-shaped-key` stops being a 51-line document and becomes the
one thing it is actually asserting:

```json
{
  "sites": {
    "site0": { "object_storage": { "access_key_id": "AKIAIOSFODNN7EXAMPLE" } }
  }
}
```

A base config plus a per-case patch, merged at test time. Adding a field to
the config then touches the base and nothing else, and a new case is three
lines rather than fifty.

### What makes it non-trivial

Three constraints, all of which need settling before any of this is written:

1. **`tofu test` takes a file path.** `var.config_path` points at a real file,
   so patches have to be materialised into merged documents before the run -
   a generator step in `task test`, with generated output gitignored. HCL has
   `merge()` but no deep merge, so doing it inside the test file is not the
   easy path it looks like.
2. **Both sides read the same corpus.** `manifest.json` already indexes every
   case for the HCL and Go halves, and it is the natural home for the patches
   themselves. That is an opportunity rather than an obstacle - one file would
   then describe the whole corpus.
3. **Review has to stay honest.** What is tested is the merged document, but
   what a reviewer reads is the patch. That is the trade Playwright makes too,
   and it is only safe while the base is small enough to hold in your head.

**Success criterion:** adding a field to the config touches one fixture-side
file, not ten. Adding a case is three lines, not fifty.

This belongs in this epoch because it is the same problem the epoch already
exists to solve - "write the reusable pieces once" - applied to test data
rather than to OpenTofu. The fixture corpus is the one place in this
repository where copy-paste is currently the documented workflow, and
`tests/README.md` says so in as many words: _"write the fixture, add an entry
to `manifest.json`, and add a `run` block of the same name."_

## Known driver: de-bloat

This repository has more files than it has ideas. The sensitive-path tripwire
arrived as six of them - a data file, two shell scripts, a Go test, a document
and a workflow - for one feature whose whole logic is under sixty lines. Two of
the six were never justified: a four-line script that belonged inline in the
workflow that called it, and a `docs/` page restating what the data file's own
header comments already said.

**The rule going in: a new file has to earn itself.** Length that would be
unreadable inline earns one. A linter or a test that only sees real files earns
one. Genuine reuse from more than one caller earns one. "It is a separate
concern" does not, when the concern is four lines - two small scripts that are
always invoked together are one script, or none.

The audit is a file-count pass over `.github/`, `scripts/` and `docs/` asking
of each file: what would break if this were three lines inside the thing that
calls it? Start with the tripwire, because it is the freshest example and the
one whose sprawl is best understood.

Two things worth deciding rather than assuming while doing it. Documentation
beside a thing tends to drift from the thing; header comments in the file being
edited are read by whoever is editing it, and a `docs/` page usually is not.
And shellcheck coverage is a real benefit of a script over an inline block, but
it is not worth a whole file for a handful of lines - the question is what the
coverage is buying, not whether it exists.

## Open questions to settle first

- Which epoch-01 resources genuinely want to be modules, versus staying
  single-use in `management/cluster`?
- The fixture corpus above: generated merged documents, or a deep merge done
  in HCL? The first needs a build step and a gitignore entry; the second needs
  a merge function OpenTofu does not have.
- Module versioning: relative path in-repo, or tagged and pinned?
- Does the self-hosted runner have what these modules need at apply time?
  `deploy-infrastructure.yml` path-filters on `modules/infrastructure/**`, so
  a change here triggers a real apply.

## Decisions

_Record as made._

## Outcome

## Deferred

## Gotchas
