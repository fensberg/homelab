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

**A second acceptance test, and this one is executable.** Every fact has one
building block, so a change to it reaches every consumer. The class it closes
was found the hard way: the integration tier kept its own copy of the config
types, the config changed shape, the copy did not, and the nightly reported
healthy backups as broken. `tests/go/repo/building_blocks_test.go` holds the
line - one reader per document, no repository root found by counting
directories, and every string restated across Go modules declared in
`tests/building-block-debt.yml`. That list must be empty, or every entry left
in it justified, before this epoch closes.

**A third acceptance test: a workload leaves by one directory delete and one
config edit.** Set by the owner on 2026-09-24: removing Valheim should be
deleting its application directory and removing its entry from the
environment's config, after which every reference to it - tests, guards,
vault requirements, delivery automation - is simply gone and everything still
passes. The same property, read the other way, is what makes adding the
second workload cheap.

Measured the same day, it is far from true. Outside the epoch records, 29
files name Valheim and about as many again reach it indirectly (the game
server's address, Steam's app id, Valve's endpoints). They fall into six
places the workload has leaked out of its directory:

- **OpenTofu.** `management/cluster/workloads.tf` creates its namespace and
  both of its Secrets, `variables.tf` reads `workloads.valheim`, and
  `tunnel.tf` routes the game server's address.
- **The vault and the config template.** `config/management.tpl.json` requires
  four `op://homelab/valheim/*` fields, and the contractor generates one of
  them by a reference written into `secrets.go`.
- **Flux.** `clusters/management/releases.yaml` and `workloads.yaml` name it
  (`workloads-production` is Valheim's Kustomization in all but name), the
  environment's overlay list includes it, and cloudflared routes to it.
- **Delivery.** `expedite.yml` is Valheim's update automation from end to
  end (Steam app id, SteamCMD, the pin file), `procurement expedite-check`
  watches its news feed, `superintendent enforce-standing-order` defaults to
  its Deployment, `scripts/work-orders.json` orders it, and the suppliers list
  carries Valve's endpoints for those jobs.
- **Tests.** Five test files name it, and 26 mutation-ledger entries point at
  its files - deleting the directory would break the ledger rather than
  retire its entries.
- **Scanner configuration.** `.trivyignore.yaml` and `.checkov.yaml` carry
  exemptions written for it.

What it would take, in outline: a workload declares everything it needs
inside its own directory - its manifests and image, its tests and ledger
entries, the secrets it needs and which of them the estate generates, its
tunnel route, its upstream and how to watch it, its work order - and every
estate-level mechanism discovers workloads by walking that directory rather
than naming them. The environment's config lists which workloads it runs and
which release of each.

Executable form, to be built with the work rather than now: a guard that
walks every workload directory and fails on any file outside that directory -
other than the environment's config - that names the workload. It fails
today, on all of the above; the epoch closes when it passes.

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

**Correction, re-measured 2026-09-21: the last clause of that sentence is no
longer true, and the table is three files short.** The seam was clean when it
was written and has degraded since, silently, because nothing was watching it.
Five files now carry `kubernetes` resources where two did, and `tunnel.tf`
straddles the boundary outright - `kubernetes_secret.tunnel_token` reads
`cloudflare_zero_trust_tunnel_cloudflared.estate.tunnel_token`, so one file
holds resources from both layers and a dependency that crosses between them.

The current inventory, and the full design that follows from it, is below under
"Decisions" - see "The split is two roots sharing one config, and the seam is
two values". The claim is left standing here because the way it decayed is the
useful part: a seam asserted in prose and guarded by nothing drifts at the rate
work is done near it.

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

## Known driver: node tailnet membership does not give pods a path

Recorded because it was built, shipped, and did not work - and the reasoning
that led there was wrong in a way worth keeping.

The cluster nodes carry the tailscale extension and appear in the tailnet as
tagged devices. A pod on those nodes still cannot reach a tailnet address:

```text
nc -zv -w5 <hypervisor tailnet address> 8006   Connection timed out
nc -zv -w5 <hypervisor tailnet address> 22     Connection timed out
```

Run from inside a pod. Both ports, so it is not about the Proxmox API.

The likely mechanism is that Flannel masquerades pod egress to the node's
primary address rather than its tailscale address, so packets reach `tailscale0`
with a source that is not a tailnet IP and are dropped. A node being a peer lets
_the node_ talk to the tailnet; it does not carry everything behind it. This
should be confirmed against the node itself before anything is built on it -
`talosctl` is the only way in, since Talos has no shell.

### The choice that was made, and why it was wrong

Two ways to put the cluster on the overlay were considered: Talos node
extensions, and the Tailscale Kubernetes operator's egress proxy. Node
extensions were chosen on the grounds that the operator makes the hypervisor
reachable through an in-cluster Service, so the endpoint would differ depending
on where the code runs and one host would end up with two addresses.

That objection was real and is now answered by something else: the config should
hold a name resolved by split-horizon DNS, so the endpoint is one value
regardless. The reason for rejecting the operator dissolved, and the option that
was rejected is the one that actually delivers pod egress.

The deciding constraint was never the endpoint. It was whether pod traffic can
reach the tailnet at all, and that was assumed rather than tested.

### Correction: this was measured against a host that was offline

**The finding above is not established, and the reasoning built on it does not
stand.** It is left in place rather than deleted because the correction is the
more useful record.

The evidence was `nc` from inside a pod to the hypervisor's tailnet address,
timing out on both 8006 and 22, read as "both ports, so it is not about the
Proxmox API". That inference is sound only if the destination was reachable at
all. It was not: the hypervisor had dropped off the tailnet, for reasons
recorded in [`01-ignition.md`](01-ignition.md). Both ports time out identically
when the host is not there, which is exactly what "both ports" was taken to
rule out.

So the Flannel masquerade mechanism is a hypothesis with no evidence behind it,
not a measured result. It may still be true - a pod reaching a tailnet address
is a real question and the answer is not obviously yes - but it has not been
tested, and the test is only valid while the hypervisor is a live tailnet peer.
That is now the first precondition of running it.

The decision reasoning above is affected in proportion. The argument that the
Tailscale operator should be reconsidered rested on node extensions having been
shown not to deliver pod egress. Nothing has been shown. The separate point
that the original objection dissolves once the config holds a name resolved by
split-horizon DNS still stands on its own, because it was never about this
measurement.

### Re-measured against a live peer: still unreachable

The test was re-run once the hypervisor was back on the tailnet, and it gives
the same answer it gave before: a pod cannot open 8006 on the hypervisor's
tailnet address. This time the destination was a live peer, so the result means
something.

That does not restore the original finding wholesale - the reasoning in it
("both ports, so it is not about the Proxmox API") was invalid and stays
retracted - but the headline claim now has one valid measurement behind it
rather than none.

**What is still unmeasured, and must not be assumed again.** Whether the
_node_ can reach the hypervisor over the tailnet has never been tested. Pod
Security Admission refuses the host-network pod that would answer it, correctly,
so the measurement has to come from somewhere else - `tailscale ping` from the
hypervisor to a node exercises the same path from the other end and needs no
privileged workload. Until that is run, "pods do not inherit node membership"
and "the nodes are not really on the tailnet" both fit the evidence equally,
and they have completely different fixes.

### Settled: the nodes do not carry tailnet traffic, and the CNI was never the subject

Measured from the hypervisor, which is itself a tailnet peer, so no privileged
workload was needed and Pod Security Admission was not in the way:

```text
root@hypervisor:~# tailscale ping site0-cp-100
ping "100.64.0.10" timed out            (x10)
no reply
root@hypervisor:~# tailscale ping site0-cp-101
ping "100.64.0.11" timed out             (x10)
no reply
```

**Peer to peer, and no reply.** No pod, no CNI, no Flannel anywhere in that
path. The nodes appear in the admin console as online, tagged devices - so
they authenticate and hold a control-plane session - and then answer nothing on
the data plane.

That resolves the question this section has been circling. It is not that pods
fail to inherit node membership. **The membership itself is registration
without reachability.** Every conclusion that pointed at the CNI, this file's
original finding included, pointed at a layer that was never involved.

**Cilium is not the fix for this, and nothing here justifies rebuilding the
cluster.** Cilium remains required for NetworkPolicy enforcement, which is a
separate argument recorded in [`03-workload.md`](03-workload.md) and untouched
by this - but the urgency that came from believing it fixed the converge is
gone, and the rebuild that was being planned to deliver it has no basis.

**The leading hypothesis, recorded as a hypothesis.** A tailscale container
that runs in userspace-networking mode registers with the coordination server
and creates no TUN device, which presents exactly this way: online in the
console, unreachable on the wire. The extension is configured in `talos.tf`
with an auth key, tags and an empty route list, and says nothing either way
about userspace mode. That is worth checking first because it is cheap and it
fits, but it is not established, and this epoch has already paid for the habit
of building on the most plausible-sounding explanation.

### The local path does not work either, and the VRF claim survives testing

The controlled probe, run from a pod with two subjects and two controls in the
same run:

```text
FAIL  hypervisor-via-tailnet     100.64.0.1:8006
FAIL  hypervisor-via-lan         <hypervisor local address>:8006
OK    public-internet            1.1.1.1:443
OK    cluster-api-on-sdn         10.10.10.100:6443
```

The controls are what make this readable. **Pods have working egress** - a pod
opened a socket to the public internet, which means Flannel is doing its job
and every version of "the CNI cannot get traffic out" is wrong. The SDN path
works too. Only the hypervisor is unreachable, and it is unreachable by _both_
of its addresses.

**That confirms the assertion in `talos.tf`**, which is worth stating plainly
because most of tonight's assertions did not survive contact:

> the node subnet lives in an EVPN VRF, and a VRF cannot deliver to a local
> address in another VRF ... so no amount of routing makes it reachable from a
> pod

Traffic from a pod transits the hypervisor perfectly well - that is what the
public-internet control proves - and still cannot be delivered to the
hypervisor's own management address, because the listening socket is in a
different VRF from the arriving packet. Transit and delivery are different
things, and this is the case that separates them.

**So the overlay is not an accident of history in this path; it is the only
path there is.** The previous section asked whether the cluster needs the
overlay to reach the hypervisor at all, on the grounds that the workstation is
no longer remote. The answer is yes for anything running _inside_ the cluster:
the local address is not merely inconvenient from there, it is unreachable by
construction. The overlay data plane has to be repaired rather than routed
around.

That does not apply to the workstation, which is not in the cluster and reaches
the local address directly - and this is precisely the "one host, two
endpoints" problem this file already records. The endpoint that works depends
on where the caller is standing: a pod must use the overlay, the workstation
must use the local address, and neither can use the other's. A single address
in the site configuration cannot be right for both, which is the argument for
the field holding a **name** resolved by split-horizon DNS rather than an
address, made here by measurement rather than by preference.

### The overlay may not need to be in this path at all

The stronger question, and it is a design question rather than a bug.

This epoch already records that "the overlay network is load-bearing for
ignition only because the workstation is remote", and that running from a
machine on the hypervisor's own network "removes that dependency and leaves the
overlay network for remote access, which is what it is actually for". That
condition has quietly become true: the workstation is now a virtual machine
beside the estate on the site network, not a remote laptop, and it is not a
tailnet member.

The site configuration holds the hypervisor's **tailnet** address where it
could hold the address it answers on locally. Everything that has failed
tonight failed on the tailnet path; nothing has yet been shown to fail on the
local one, because nothing has tried it.

So before repairing the overlay data plane, it is worth measuring whether the
cluster needs the overlay to reach the hypervisor at all. `talos.tf` asserts it
does - that an EVPN VRF cannot deliver to a local address in another VRF, so
"no amount of routing makes it reachable from a pod" - and that assertion has
never been tested. If a pod can open the API on the local address, the overlay
comes out of this path entirely, and with it the whole class of failure that
consumed this session.

### Not userspace networking: the TUN device exists and the service is running

The first thing the new `talosconfig` verb was used for, and it disproved the
leading explanation rather than confirming it:

```text
NODE           TYPE         ID           TYPE   KIND   OPER STATE   LINK STATE
10.10.10.100   LinkStatus   tailscale0   nohdr  tun    unknown      true

ID       ext-tailscale
STATE    Running
HEALTH   ?
EVENTS   [Running]: Started task ext-tailscale (PID 2377) ... (5h31m ago)
```

`tailscale0` exists, is a `tun`, and its link state is up. A userspace-mode
tailscaled creates no TUN device at all, so that hypothesis is dead. The
extension service has been running for five and a half hours without
restarting, so it is not crash-looping either.

Which leaves a node that has a control-plane session, a tagged registration
visible in the admin console, and a working tunnel interface - and still
answers nothing from another peer. The interface existing is not the same as
the interface being addressed and routed, and neither of those has been looked
at yet.

Worth noting `HEALTH ?` rather than a healthy marker. Talos reports unknown
health for an extension service that declares no health check, so this is
probably not a signal in itself - but it does mean the estate has no health
signal at all for the component the whole overlay depends on, which is the same
observability gap recorded against the hypervisor in
[`01-ignition.md`](01-ignition.md).

**The verb earned itself immediately, and that is the wider point.** This
question had been unanswerable all session; the answer took one command once
the credential could be rendered. Two hypotheses had been built on top of the
unanswerable version of it, and both were wrong.

### Addressing and routing are correct: the failure is in the data path

With the node finally inspectable, everything below the WireGuard transport
checks out. Recorded as ruled out, because each of these was a candidate:

```text
AddressStatus   tailscale0/100.64.0.10/32                100.64.0.10/32
AddressStatus   tailscale0/fd7a:115c:a1e0::aaaa/128    (the tailnet ULA)

RouteStatus     52/inet4//100.64.0.1/32/0     -> tailscale0     (the hypervisor)
RouteStatus     52/inet4//100.64.0.11/32/0    -> tailscale0     (a sibling node)
RouteStatus     52/inet4//100.64.0.12/32/0    -> tailscale0     (a sibling node)
RouteStatus     52/inet4//100.100.100.100/32/0  -> tailscale0     (MagicDNS)
```

- **The interface is addressed.** `tailscale0` carries the tailnet address the
  admin console lists for this node, and the ULA. An unaddressed tunnel would
  have explained everything; it is not that.
- **The routes exist**, one per peer, in Tailscale's own table 52 - including a
  route to the hypervisor specifically.
- **The control plane is live.** The log shows a peer's disco key changing at
  the moment the hypervisor's daemon was restarted, followed by
  `wgengine: Reconfig: configuring userspace WireGuard config (with 3 peers)`.
  The node learned about that restart within seconds, so it is talking to the
  coordination server continuously.

Note that "userspace WireGuard" in that line is not the userspace-networking
mode ruled out above - `tailscaled` always runs the WireGuard implementation in
userspace. The TUN device is what distinguishes the two, and it is present.

**So the remaining suspect is the transport.** Registration, configuration,
addressing and routing are all correct, and two peers that agree about each
other still exchange nothing. The open question is whether either end has a
working path at all: `netcheck` on the hypervisor reported **UDP is blocked**,
which forces every peer pair onto a DERP relay, and the hypervisor's relay
connections were broken for hours by the IPv6 fault recorded in
[`01-ignition.md`](01-ignition.md). Whether the nodes ever established relay
paths of their own has not been looked at.

### The extension's log is unreadable without filtering

An operational finding rather than a defect, and it cost time. `ext-tailscale`
logs a line every fifteen seconds, indefinitely:

```text
localapi: [POST] /localapi/v0/debug
```

Something polls that endpoint on a timer, and the result is that the log is
almost entirely that one line. The three lines that mattered - a disco key
change and the WireGuard reconfiguration - were buried in hundreds of them.

The hypervisor's journal named its own fault four times a minute and was read
in seconds; this one hides its content in noise. Anything reading these logs
should filter for `derp`, `magicsock`, `netcheck`, `peer` or `endpoint` rather
than tailing them, and a health check that reads this log needs to know that
volume is not liveness.

### Resolved: pods reach the hypervisor, and the CNI was never involved

Re-run after the hypervisor's routing rule was fixed, from an ordinary pod with
no host networking and no special placement:

```text
pod -> <hypervisor tailnet address>:8006     REACHABLE
```

The same probe that returned `UNREACHABLE` all session now succeeds, and
nothing about the cluster changed between the two runs. One `ip rule` on the
hypervisor was the entire difference.

**So every finding in this section that pointed at pod networking is closed,
and none of them were true.** Not Flannel, not the CNI, not pods failing to
inherit node membership, not the extension. The chain was:

1. The hypervisor's IPv6 stack was enabled with no addresses on it, so the
   daemon dialled a family it could not send from and lost its control-plane
   session. The host left the overlay.
2. Disabling IPv6 at the stack restored registration, and the console showed
   the host online - which looked like recovery and was not, because the relay
   addresses come from a map of literal addresses rather than from a resolver.
3. The data plane stayed dead because a marked reply for the host's own address
   was routed by rules that could not deliver it locally - the collision
   between the SDN's VRF moving the `local` table and the overlay's own rules
   landing before it.
4. With that fixed, the hypervisor has transport, and everything downstream of
   it works: peer to peer, and from inside a pod.

**The consequence for this file's original finding is that it is withdrawn
entirely rather than merely re-evidenced.** Node tailnet membership does give
pods a path. It never did not.

**And the consequence for epoch 01 is that its acceptance test is unblocked.**
The converge halted at Verify because it could not reach the hypervisor on the
Proxmox API port; a pod can now do exactly that. Nothing else was in the way.

The Cilium requirement recorded in [`03-workload.md`](03-workload.md) is
untouched by this, for the reason recorded there: Flannel does not enforce
NetworkPolicy, which is a property of Flannel and has nothing to do with
reachability. It stopped being urgent last night and it has not stopped being
required.

### A test must prove its own preconditions

The durable lesson from the hours this cost. The test used was:

```text
pod -> <hypervisor tailnet address>:8006   ->  timed out
```

It cannot distinguish "there is no path" from "there is nothing there", and it
was read as the first while the second was true. Every subsequent design
decision inherited that.

A reachability test in this estate should therefore carry a control in the same
run: something known to be reachable from the same place, so a failure of the
subject is visible against a success of the control. A pod probing a tailnet
address should also probe a public address and a cluster-local one. If all
three fail, the pod has no egress at all and the tailnet is not the subject; if
only the tailnet one fails, the result means what it appears to mean.

This is the same rule as "measure from where the traffic starts", extended to
the other end: establish that the destination is answering before drawing
conclusions from it not answering.

### Topology note: the workstation is not on the tailnet

Worth recording because it is easy to assume otherwise and it changes what a
test from there proves. The development workstation is a virtual machine beside
the estate rather than a member of the overlay - it reaches the cluster API and
the state database over the site network directly. Only the hypervisor and the
cluster nodes are tailnet members.

So a successful `task kubectl` from the workstation says nothing about the
overlay, and the workstation cannot be used to test tailnet reachability at
all. The two machines that can are the hypervisor and the nodes.

**The process finding is the durable half, and it is the same one as before,
one level up.** The earlier entry says: measure from where the traffic starts
before designing what carries it. This adds: establish that the destination is
answering before drawing conclusions from it not answering. Four confident
wrong diagnoses became five, and the fifth was built on a test whose
preconditions nobody checked.

### What remains open

- **A forwarder on the hypervisor**, bound inside `vrf_internal`, forwarding one
  port to the local API. Small, expressible in Ansible, and grants access by
  subnet, which Flannel cannot narrow.
- **The Tailscale operator's egress proxy**, ACL-scoped, and no longer carrying
  the objection that ruled it out.

Node membership is not wasted either way - it is how a node reaches anything
else on the tailnet - but it is not what unblocks a converge.

### The process finding

The network work in this epoch produced a sequence of confident, plausible, and
wrong diagnoses: a firewall rule (the firewall was disabled), a route leak (the
destination was local, so there was nothing to route to), the SDN gateway
address (no listener in that VRF), and node tailnet membership (pods do not
inherit it). Each was disproved by a single command that could have been run
first.

The pattern is that each hypothesis was reasoned from the layer that had just
been ruled out, rather than from a measurement of the layer in question. What
finally settled each one was a direct test from the exact place the traffic
would originate - `ip vrf exec` on the host, and `nc` from inside a pod.

Worth stating as a rule for this tier: **measure from where the traffic starts,
before designing what carries it.** Network diagnoses are unusually cheap to test
and unusually expensive to get wrong, because a wrong one is not idle - it gets
built, merged, and rebuilt on.

## Known driver: the config records an address where it should record a name

`hypervisor.nodes[].ip` holds a single address, used for three different things:
Ansible's SSH target, the Proxmox API endpoint the OpenTofu provider dials, and
the Verify phase's reachability check. One value, three consumers, and they do
not all live on the same network.

That is why putting the cluster nodes on the overlay did not make a converge
work. The nodes became tailnet peers, but the address being dialled was still
the hypervisor's LAN address, so traffic routed out through the EVPN VRF - the
path that cannot deliver to it - rather than over the overlay. Membership only
helps if the thing being dialled is a tailnet address.

The workaround is to put the tailnet address in that field. It works, because
every consumer is on the tailnet, but it is wrong in a way worth naming: it
couples the workstation and Ansible to tailscale being up on the hypervisor, and
it hard-codes one topology into a field that several different networks read.

### The fix is a name, not an address

The field should hold a hostname - `hypervisor.<site>.<domain>` - and DNS should
answer it differently depending on who asks. The tailnet's resolver returns the
tailnet address; the LAN's resolver returns the LAN address. Split-horizon
resolution is the ordinary answer to exactly this problem, and Tailscale's split
DNS supports it directly.

Three things follow from it, and they are the reason this belongs in an
abstraction epoch rather than being filed as a bug:

**The config stops encoding topology.** Today the value silently asserts "every
consumer of this field shares one network". A name asserts nothing; the resolver
decides, and each consumer gets the best path available to it.

**No consumer depends on another's network being up.** With the tailnet address
in the field, a workstation on the same LAN as the hypervisor still routes
through the overlay to reach it, and loses it entirely if tailscale is down.
With a name, it resolves locally and goes direct.

**It survives a second site.** Each site's hypervisor gets its own name, and
adding a site adds a DNS record rather than a decision about which address to
write down.

The cost is a dependency on DNS being right, which is a real one - a wrong or
missing record fails in a way that looks like a network problem. Verify already
distinguishes "cannot resolve" from "cannot reach" badly, and would want to
distinguish it well.

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

### Ignore lists are the same problem, one line at a time

A file count is the visible half. The other half is the entries that
accumulate inside `.gitignore`, `.prettierignore`, `.github/super-linter.vars`
and `scripts/approved-suppliers.yml`'s exemptions - each one added for a real
reason, none ever removed, and collectively a description of the repository
nobody has read end to end.

They are worse than an extra file, because an extra file is at least obvious.
An ignore entry silently narrows what a check covers, and the narrowing
outlives the reason: three `.gitignore` lines for pre-toolshed build paths are
already dead the day the last such branch closes, and nothing will notice.

**So every entry has to carry the condition that would remove it**, and the
audit asks that of each one: what has to become true for this line to go? If
there is no answer, the entry is not an exception, it is a decision nobody
wrote down. Known entries with their conditions already recorded:

| Entry                                                         | Removed when                                                  |
| ------------------------------------------------------------- | ------------------------------------------------------------- |
| `.gitignore`: `scripts/{contractor,signedpush,survey}/<name>` | every branch predating `toolshed/` is merged or closed (#167) |
| `.prettierignore`: `pnpm-lock.yaml`                           | never - a lockfile's format belongs to its package manager    |

The second is there to make the point that "never" is a legitimate answer.
What is not legitimate is silence.

## Known driver: the estate runs at a few percent, and the fuse is memory

The worker pool above is stated as a prerequisite for autoscaling. It is more
urgent than that: without it every workload in the estate runs on the machines
holding etcd quorum, and there is nowhere else for anything to go. This section
records what that costs, measured, and the model that decides what to build.

### What was measured, 2026-09-05

Taken with `talosctl memory`, `talosctl stats` and `kubectl describe node`
against the live cluster, 24 hours after it came up.

| Node           | Used    | Available | Requests committed | CPU requests |
| -------------- | ------- | --------- | ------------------ | ------------ |
| `site0-cp-100` | 1136 MB | 2387 MB   | 1342 Mi (41%)      | 760m (19%)   |
| `site0-cp-101` | 1276 MB | 2318 MB   | 1202 Mi (37%)      | 560m (14%)   |
| `site0-cp-102` | 1271 MB | 2323 MB   | 1266 Mi (39%)      | 610m (15%)   |

No `MemoryPressure`, `DiskPressure` or `PIDPressure` on any node. Talos's own
services are a rounding error - `apid`, `trustd` and the tailscale extension
together are about 133 MB and burned roughly 150 CPU-seconds across the whole
day, which is 0.2% of one core. The hypervisor reported 3.88% CPU across its
18 cores at rest.

**So 4 cores and 4 GiB is a well-judged size for a control-plane node, and the
control planes are not what is held back.** They were sized by guess and the
guess was good. This is recorded because the obvious reading of the OOM kill
below is "the nodes are too small", and that reading is wrong.

### The ceiling, not the total, is what broke

Summed, the three nodes have about 7 GiB free. But it is in three pieces and
no piece exceeds ~2.3 GiB - and by scheduler accounting it is tighter still,
because requests are already committed: **the largest memory request a new pod
can have accepted on any node is about 1.9 GiB.**

That is the whole of the OOM kill recorded in #236. A Go build compiling
Terratest's dependency tree wanted more than 1.9 GiB, was `BestEffort` because
it had requested nothing, and Talos's OOM controller selected exactly the
cgroup it is designed to select:

```text
[talos] OOM controller triggered
[talos] Sending SIGKILL to cgroup {"cgroup": "/sys/fs/cgroup/kubepods/besteffort/pod..."}
```

The guard worked. The estate gave it no better option.

**`Taints: <none>` on all three nodes** is the line that matters most in that
output. There is no workload tier - there is a quorum tier that also takes
walk-ins.

### The model: a domestic electrical panel

The operator's framing, and it is better than an analogy, because electrical
practice has already solved this problem and the vocabulary transfers almost
term for term. It is adopted here as the estate's way of talking about
capacity.

| House                                                    | Estate                                     |
| -------------------------------------------------------- | ------------------------------------------ |
| Service capacity - the panel's rating                    | Node `Allocatable`                         |
| **Connected load** - the sum of every nameplate          | Sum of `limits`                            |
| **Demand load** - what is assumed to run together        | Sum of `requests`                          |
| **Demand factor** - the bet that they will not all peak  | Setting requests deliberately below limits |
| A breaker trips                                          | OOM kill, or kubelet eviction              |
| **Load management** - shed the car charger under load    | `PriorityClass` and preemption             |
| **An interlock** - two loads that may never run together | A queue depth or concurrency limit         |
| Time-of-use scheduling                                   | A cron schedule into an overnight lull     |

The goal it expresses: the hypervisor is paid for whether or not it is busy, so
it should be close to fully committed and never tripping. Not "leave room to be
safe" - be deliberate about what runs together.

### There is only one fuse in this house, and it is memory

The single most useful consequence. CPU has no breaker: over-draw it and work
runs slower, which is degradation rather than failure. Memory has one, and it
fires by killing a process.

So there is not one utilisation target, there are two regimes:

- **Pack CPU hard and deliberately.** 100% is the goal, not the hazard.
- **Keep genuine headroom on memory.** 100% is an outage.

Every decision below follows from that split, and conflating the two is how a
plausible capacity plan produces an estate that trips.

### Overcommit in exactly one place: inside Kubernetes

**Hard-allocate memory at the hypervisor; overcommit it inside Kubernetes.**

Specifically, **do not enable memory ballooning on these VMs.** It is the
obvious hypervisor-level lever and it is a trap: the kubelet computes
`Allocatable` from what it observes at boot and never revisits it. Balloon a
node down afterwards and the kubelet keeps scheduling against memory that no
longer exists, with no way to learn otherwise. The kubelet is the only party
that knows what is running and can evict deliberately, so it has to be the
layer that is told the truth.

vCPU is the opposite and may be overcommitted freely at the hypervisor, because
contention degrades rather than kills. Ordinary practice is 2-4x; the estate is
currently at 0.67x, with 12 vCPU allocated across 18 cores.

Proxmox `cpuunits` is the hypervisor-level counterpart to `PriorityClass`: a
cgroup weight per VM, so CPU contention resolves in favour of quorum
automatically. The same principle expressed at two layers.

### Shedding and interlocking are different tools

Worth separating, because the first draft of this reasoning treated them as one
and they answer different questions.

- **Load shedding is right when the loads differ in importance.** CI against
  etcd. The estate wins, CI is dropped, and a `PriorityClass` expresses it.
- **An interlock is right when they are equally important and must take
  turns.** Two heavy integration runs have no claim over one another;
  preempting either to run the other is pure churn. What is wanted is a queue.

This changes what `maxRunners` is for. It reads as a cap to be raised as
capacity grows, and it is not - **it is the interlock**, and its correct value
is how many heavy jobs fit in the workers at once.

### Eviction is not free here, so prefer admission control

The one place the electrical model breaks, and it changes the design rather
than decorating it. A shed load resumes: pausing a car charger costs time and
nothing else. **An evicted job loses its work.** A CI job killed at minute
twelve of thirteen throws away twelve minutes and starts over.

So preemption is the backstop that protects quorum, which is what it is for.
The primary mechanism for CI against CI is **not admitting the job**, because a
job that never started is cheaper than one killed near the end.

### Three priority classes, sorted by what tolerates delay

The operator's second model, and it supersedes the heavy/light split this
section first proposed. That one sorted work by size on a security axis - jobs
with estate access against jobs without - and let latency ride along with it.
Sorting by **how much delay the work tolerates** is the property the scheduler
actually acts on, and it produces three classes rather than two:

1. **`critical`.** Tolerates no delay at all, and may evict running work to
   avoid one. etcd, the API server, CoreDNS, the state database, Flux, and the
   runner listener.
2. **`interactive`.** Somebody is waiting for it, so queueing is the entire
   cost - the fast pull request checks, where the job itself is 40 seconds and
   a two-minute wait is all the user-visible latency there is.
3. **`batch`.** Must complete; need not complete now. Integration runs,
   converges, image builds, state backups, the clerk's sweep, dependency
   updates.

The assignment is by tolerance, not by importance. A state backup is one of the
more important things the estate does and it is `batch`, because nothing
observes its latency.

**The names are the field's own, and that is deliberate.** The model was
arrived at through an analogy - emergency vehicles, commuters and logistics on
a road - which is a good way to think and a bad way to name. `critical` is the
word Kubernetes itself uses for this (`system-cluster-critical`), and
interactive-against-batch is the standard distinction for work somebody is
waiting on against work that merely has to finish. This record keeps the
reasoning; the cluster gets the vocabulary someone else would already know.
The estate's own naming rule decides it: a thematic name must never obscure a
real term of art.

#### Jumping the queue is not the same as taking someone's slot

The refinement that makes this implementable, and it is not obvious. Kubernetes
priority does two things at once - it decides who gets the next free slot, and
it decides who gets evicted to make one - and these three want those
answers to differ.

An interactive job should get the next free slot ahead of a batch job. It
should **not** be able to evict one already twelve minutes into a
thirteen-minute run, which is exactly the trade the "eviction is not free"
decision above refuses. Preempting a long job to admit a 40-second one destroys
twelve minutes of work to save two minutes of waiting.

`PriorityClass` separates the two answers. `preemptionPolicy: Never` places a
pod ahead of lower-priority _pending_ pods while never evicting a _running_
one:

| Class         | `value` | `preemptionPolicy`     | Yields to         |
| ------------- | ------- | ---------------------- | ----------------- |
| `critical`    | 1000000 | `PreemptLowerPriority` | nobody            |
| `interactive` | 10000   | **`Never`**            | `critical`        |
| `batch`       | 100     | `Never`                | both of the above |

So eviction exists in exactly one place, which is the smallest surface that
still protects quorum, and matches this record's preference for admission
control over eviction everywhere else.

The values are round and widely spaced because nothing computes with them - they
only have to sort. All three are above the zero an unclassified pod gets, which
is deliberate: an unclassified pod is a workload nobody decided about, and it
should rank below the work that was deliberately marked as able to wait.

#### A priority is a rule about yielding, not a partition

Worth stating because the model invites the wrong reading. Three dedicated node
pools would be walls, and walls strand capacity in whichever pool happens to be
idle - the opposite of the goal that produced this whole section. A priority
class reserves nothing: when the cluster is quiet, `batch` work uses every core
there is, and gives ground only when something above it needs the room.

**One pool of workers, three priority classes.** The one genuine partition is
the one already designed, and it is drawn by the anti-control-plane affinity
above rather than by the priority classes.

**Retraction, and this sentence used to say the opposite.** It read that the
top class lives on the control planes and the other two share the workers -
which is wrong
and contradicted this record's own build order four sections down - the same
order that lists Flux and the runner listener among the things to move OFF the
control plane, while the lane table names both as lane 1. Both could not be
right.

The error was conflating two axes this model exists to separate. **A priority
says when a pod yields; placement says which machine it sits on.** `critical`
means nothing may delay it, and that is enforced by a `PriorityClass`, which is
cluster-wide and says nothing about nodes. Almost every `critical` workload -
Flux, both ARC pods, the CloudNativePG operator, the OpenEBS provisioner - is a
small always-on controller with no reason to sit beside etcd, and each one that
does is unreserved memory a heavy job can take.

So the partition is: **the control plane carries only control-plane work**, and
everything else shares the workers under three priority classes. The two
genuine exceptions stay for a storage reason rather than a scheduling one -
the state database's volumes are pinned to their nodes, and CoreDNS is
Talos-managed - and both are `critical` while sitting where they sit.

The security axis does not force a second partition either. Fork-run jobs must
not be able to _reach_ the estate, which is a NetworkPolicy question and
therefore waits on Cilium - see [`03-workload.md`](03-workload.md). It is not a
question about which node they sit on. So the earlier claim that two independent
arguments demanded the same structure was half right: they demand separate
**credentials and network policy**, and separately a priority. Those are
different mechanisms and conflating them was the error in the first draft.

#### `batch` covers two things that differ in one way

Work that must eventually complete - an integration run somebody is waiting on
the result of - wants bounded retries. Work that is idempotent and catches up
does not: a missed state backup or dependency sweep is repaired by the next
scheduled run doing the same thing. Both tolerate delay equally, so both are
`batch`, but only the first needs a retry when it is killed. Narrow, and real.

#### This governs infrastructure operations too, not only CI

Creating a virtual machine is `batch`. It must complete - a worker that never
appears is a failure - and nothing observes its latency, because nobody is
waiting on the second one. The same is true of a converge, a state backup and an
image build. That the model extends past CI jobs to the estate's own operations
is worth stating, because it is what makes it a scheduling policy for the estate
rather than a CI feature.

The consequence is a pleasing inversion: **`batch` work is what builds the
capacity the other two consume.** The slowest work builds the room for the rest.

#### Class by class, what is actually short

Measured rather than assumed, because the three are not short in the same way
and the remedies are different.

**`critical` work has capacity and no protection.** The control planes use about
1.2 GiB each, so it is not short of room. The denominator in that sentence used
to read 3.8 GiB and was wrong: 3.8 GiB is what the guest reports, while what the
scheduler can hand out is **3281 MiB** - Talos reserves about 815 MiB of a 4 GiB
machine for itself and the kubelet. The headroom was overstated by 600 MiB. What
it lacks is
any claim on that room: `Taints: <none>`, no requests on the dispatcher or the
operators (#237), and no `PriorityClass`. Its capacity is real and entirely
unreserved, which means a heavy batch job can take it and has. **The remedy is
not more capacity, it is making the capacity it already has non-negotiable** -
which is requests and priority, not machines.

**`interactive` has no workload.** Nineteen jobs across the workflows run on
`ubuntu-latest` and five on the self-hosted scale set; every one of the eight
pull request validation lanes is in the first group. So the commuter lane has
no estate capacity at all today, and building it is a migration rather than a
resize - with the prerequisites [`01-ignition.md`](01-ignition.md) already
records, `harden-runner` not surviving the move being the substantive one.

**`batch` is the one genuinely starved.** It is the only class running in the
estate today, and it is what #236 killed.

So the workers being built serve `interactive` and `batch`, and the `critical` work is a
configuration change on machines that already exist. Those are different
efforts and only the first needs hardware.

#### The stopped template is not costing what it appears to

Recorded because `qm list` invites the misreading and it very nearly cost a
useful thing.

The per-hypervisor Talos template shows `4096` MB in `qm list` and is
`stopped`. That column is the _configured_ allocation - what the guest would
take if started - and a stopped guest consumes **no memory and no CPU**. It was
never part of the 37 GiB; the four running `kvm` processes account for all of
it. The template's real cost is 8 GB of disk on a pool at 3.42% used.

And it is load-bearing: `talos_cp` clones from it, so it is precisely what makes
creating a machine cheap. Deleting it would not free memory the estate is short
of, and would make every future VM - including the workers - re-download and
re-materialise the image first. **It makes batch work slower for no memory
back.** The right disposition is to leave it alone.

#### The model immediately finds a misassignment

Applied to what is running today, the runner listener is the **dispatcher** -
always on, tiny, and if it dies no CI runs at all - so it is unambiguously
`critical`. It was `BestEffort`, along with the ARC controller, the
CloudNativePG operator and the OpenEBS provisioner (#237), which meant the
eviction order in force put the dispatcher first.

That is the argument for #237 being a prerequisite rather than hygiene, in one
line: the model does not exist until every workload has been assigned to a
class, and an unassigned one does not land in `batch` - it lands below it.

### Retraction: ZFS ARC was not the problem

Recorded because the hypothesis was confident, cheap to state, and wrong, and
the next session would otherwise re-form it.

The hypervisor reported 37.35 GiB of 62.29 GiB used while the three VMs account
for only 12 GiB. The inference was that Proxmox's default ARC cap of half of
RAM had let the cache grow to ~25 GiB, and that capping it would be the single
cheapest large block of memory on the box. Measured instead:

```text
c          6.23 GiB
c_min      1.95 GiB
c_max      6.23 GiB
size       6.15 GiB
```

ARC is capped at 6.23 GiB - about 10% of RAM, the newer Proxmox default rather
than the older 50% - and is holding 6.15 GiB against a pool with 33 GB of data.
It is correctly sized and there is nothing to reclaim there.

**The missing memory was a fourth virtual machine.** `ps -eo rss,comm` showed
four `kvm` processes, not three: the three control planes at ~4.03 GiB each,
matching `dedicated = 4096` with no ballooning exactly as designed, and a
16 GiB build VM outside the site's id band. Legitimate, expected, and named in
no epoch record until this one. With ARC and the Proxmox daemons the arithmetic
then closes against the reported figure.

The process lesson is the one already in this repository: an arithmetic
inference about a live machine is a hypothesis, and the reading costs one
command. It was made twice in one session - first that ARC held the memory,
then that the host ran three guests - and both times the correction came from
the operator running something rather than from a tool noticing. #239 is the
structural fix: `survey` reports what should not be on a site and never what
the site can carry.

### The budget, and what it will become

**24 GiB is genuinely free**, not the ~44 GiB an earlier draft of this section
assumed. The host also runs **no swap**, so memory exhaustion there does not
degrade, it kills a guest outright - which makes headroom worth more here than
on a machine with somewhere to spill.

That settles the sizing: **two workers at 6 vCPU and 8 GiB**, taking 16 GiB of
the 24 and leaving 8 GiB of host headroom. After Talos's ~600 MiB of overhead
each worker offers about 7.2 GiB allocatable, against the 1.9 GiB ceiling that
killed the job in #236 - close to a four-fold improvement on the number that
actually broke, which is the one worth optimising.

**The build VM's 16 GiB returns eventually.** The operator's intent is that the
estate ends with no development machine at all: it exists to facilitate the
build, and that work may itself move into a worker. So the long-run budget is
nearer 40 GiB, and the workers are expected to grow into it. Resizing a worker
is cheap in a way resizing a control plane is not - a worker is not an etcd
member, so it is a drain and a restart - which is one more argument for putting
capacity into workers rather than into the control plane.

Worth noting where that work would land: a development environment is
interactive in the literal sense, so delay is its whole cost, which makes it
`interactive` rather than a fourth class of its own.

### Where the utilisation actually is

A correction to this section's own framing, made after the numbers closed. The
hypervisor is already about 60% committed on memory, so "the estate runs at a
few percent" is true of CPU and misleading about memory: 3.88% of 18 cores
against roughly two thirds of the RAM already spoken for.

That is not a coincidence, it is the one-fuse principle observed from the other
side. **The resource with no breaker is the one with all the headroom, and the
resource with the breaker is nearly committed.** So filling this box is
overwhelmingly a CPU exercise - overcommit vCPU, pack the classes, let contention
degrade - while the memory side is about spending a small fixed budget well
rather than filling a large empty one.

### What this epoch therefore builds

In order, each additive and needing no cluster rebuild:

1. **A worker machine set.** Sized so that a single node can offer a large
   contiguous block, because the defect is a ceiling rather than a total. Two
   workers rather than one, so the pool can be drained and so epoch 05 has
   something to move work between.
2. **A `nodeSelector` on the runner scale set**, pointing CI at the workers.
   Deliberately not a taint on the control planes - see the gotcha below.
3. **Requests on everything that matters**, which is #237 and #234. This is not
   hygiene, it is the load-bearing part: eviction order is driven by QoS class,
   and until it is designed rather than accidental, filling the box on purpose
   is reckless. It is also what assigns each workload a priority, and an
   unassigned one ranks below the work that was marked as able to wait.
4. **Three `PriorityClass` objects**, per the table above, with
   `preemptionPolicy: Never` on all but `critical`, so eviction exists only
   where it protects quorum.
5. **A second runner scale set**, so that `interactive` and `batch` CI have
   separate `maxRunners` interlocks and a lint job cannot queue behind an
   integration run for a slot.

Then epoch 04 measures it, and epoch 05 makes it elastic if that is ever worth
anything - which [`05-node-lifecycle.md`](05-node-lifecycle.md) records that it
currently is not.

## Acceptance tests

### A step is declared once, and every verb that shares it reads that declaration

**This epoch is not complete while `contractor plan` and `contractor converge`
can disagree about what a converge does.**

The bar: the targets a converge applies and the targets a plan plans come from
**one declaration**. Adding a step, removing one, or changing what it targets
changes both verbs at once, and neither can be edited on its own. Each step says
which verbs it takes part in; a step naming no verb, or naming a verb nothing
dispatches, fails the build rather than being skipped.

Verified by a contract test that both sequences walk the same declared list, and
by the list being the only place a resource address is written.

#### Why this belongs to this epoch and not to epoch 06

Epoch 06 asks "can this be consolidated", and on its face that is where two
verbs doing nearly the same job would go. It belongs here because this epoch
**forces the work whether or not anybody plans it**.

Turning the management root into a module moves every resource behind
`module.<name>.`, and the contractor currently names eight root-level addresses
as string literals in Go:

```text
proxmox_download_file.talos_disk_image
proxmox_virtual_environment_vm.talos_template
proxmox_virtual_environment_vm.talos_cp
proxmox_virtual_environment_vm.talos_worker
talos_machine_configuration_apply.control_plane
talos_machine_configuration_apply.worker
talos_machine_bootstrap.this
talos_cluster_kubeconfig.this
```

Every one of them breaks the moment the root becomes a module. So this epoch
cannot ship without touching all eight, and there are two ways to do it: edit
the literals in place, which costs the same effort and leaves the two verbs as
separate hand-written sequences that can drift again; or make the steps data,
which is the same edit and closes the gap permanently.

Writing it down as a criterion is the difference between those two, because the
cheaper-looking one is the one that gets done under time pressure.

#### What it is actually fixing

On 2026-09-22 a `moved` block renaming one R2 bucket resource halted every
converge on `main` at its first targeted apply, for a day. The pull request that
introduced it was green, because `contractor plan` runs one **untargeted** plan
while the converge runs **targeted** applies - and an untargeted plan of a
pending rename succeeds where a targeted one fails.

The immediate trigger is handled, and the estate keeps two hermetic guards for
it. The class is not: the two verbs are still different commands, so the
difference between them is still the region no check covers. That is #497, and
this criterion is what closes it.

The generalisation, which is the operator's and is the better statement of it:
**a block should be placeable in any verb that shares the action.** Two blocks
that do effectively the same thing are two things to keep in step, and this
estate has now paid for that twice - once in a version pinned in two files, once
here.

### The addressing scheme is computed once

Four implementations of one scheme today, across two languages:

```text
management/cluster/variables.tf              site_cidr = "10.${local.octet}.0.0/16", host_octets
scripts/contractor/config/config.go "10.%d.10.%d" for nodes and workers, the /24 and the gateway
scripts/contractor/internal/phases/sterilize.go  "10.%d.10.%d" for the state database host
tests/go/harness/harness.go                  "10.%d.10.%d" for a control plane by index
```

The bar: **one of them computes it and the rest read it.** No second
implementation, and no contract test whose job is to notice that two
implementations still agree.

This is the epoch's own known driver arriving in a different file. The record
already says a site should be "an instantiation rather than a `TF_VAR_site`
switch" - and a site is, before anything else, an address range. Four things
deriving that range independently is the copy-paste this epoch exists to remove,
sitting in the one place nobody thought to look because it is not under
`modules/`.

Note what the estate does today instead: `sterilize.go` carries a comment
admitting it restates `variables.tf`, and a contract test holds the two numbers
together. That test is the right response to a duplication you have decided to
keep. It is not a substitute for removing one.

### A helper is written once, and the module boundary is a decision

Two copies of the rclone credential mapping:

```text
scripts/contractor/internal/phases/teardown.go   r2Env
tests/go/integration/backup_health_test.go       rcloneEnv
```

Three implementations of "is this port answering":

```text
scripts/contractor/internal/run/net.go           WaitForPort
tests/go/integration/state_database_test.go      portOpen
tests/go/e2e/net_test.go                         portOpen
```

The last two are the same function twice, in two packages of one module.

The bar: **each of these exists once, and the reason it can be imported is
written down.** What makes this a design question rather than a tidy-up is the
module boundary. `scripts/contractor` is module `homelab/contractor` with zero
external dependencies, which is load-bearing - it is why the Go lanes need no
`go.sum` cache and why a dependency scan of the shipped binary finds nothing -
and `internal/` cannot be imported across a module boundary at all. `tests/go`
is a separate module that today requires nothing of it.

So sharing means promoting something out of `internal/` and letting `tests/go`
take a local `replace` on the contractor. That costs `tests/go` a dependency and
costs nothing to the contractor, whose zero-dependency property is about
_external_ packages. Worth stating explicitly, because the alternative that
looks cheaper - copying it a fourth time - is what produced this list.

### A new custom block is refused unless somebody says why

The three criteria above remove the duplication that exists today. This one is
what stops the next one, and it is the only criterion here whose absence would
make the others a one-off cleanup.

**The rule, in order.** Reuse the reusable block that exists. If none exists,
build a new one that is reusable. Only when it genuinely cannot be reusable do
we build something custom - and that is a decision somebody makes out loud, not
a default anybody falls into.

**What a machine can and cannot check.** It cannot look at a block and tell
"reusable component" from "custom block"; that is a judgement. What it can do is
**refuse an undeclared one**. So the guard is a registry plus a mandatory
declaration, and the judgement stays with the person while the _silence_ is
abolished - an omission and a considered exception must not look the same, which
is the rule `workloadPod.Unasserted` and `tests/coverage-exemptions.yml` already
apply to other questions.

The closest precedent is `scripts/approved-suppliers.yml`, and the shape is
worth copying exactly: an unapproved tool is not forbidden, it costs a focused
pull request saying what it is and why the estate should take it. A custom block
is not forbidden either. It costs a declared reason.

**Per layer, because the check differs and none of them is the whole answer:**

- **OpenTofu.** Once the root is a module, a bare `resource` block outside
  `modules/` is the exception. The guard walks every `.tf` file and fails on one
  that is neither inside a module nor declared, with a reason, as instantiating
  nothing reusable.
- **Kubernetes.** An object declared directly under `environments/` rather than
  as an overlay of a base in `modules/applications/` is the same exception, and
  is checked the same way.
- **Go.** The hard one, and the guard here is deliberately narrow: fail on two
  functions whose normalised bodies are identical across packages. That catches
  copy-paste - it would have caught `portOpen` written twice verbatim - and it
  does **not** catch the same idea reimplemented differently, which is what
  produced the four copies of the addressing scheme. Say so in the failure
  message rather than letting a green run imply more than it proves.

**The honest limit.** Nothing here can refuse a second implementation that was
written from scratch and looks different. The registry is what covers that, and
only because adding to it is a moment where somebody has to type a reason. If
that ever becomes a box people tick, this criterion has failed and the record
should say so rather than the guard being widened until it is noisy enough to
disable.

### Considered, and deliberately not made criteria

Named here so the next person does not have to rediscover why.

- **`workloadPods` restating what the manifests declare** (#492). Real, and the
  same shape, but its fix is enumeration - walk every pod-producing manifest and
  fail on an undeclared one - rather than a reusable component. It belongs to
  the guard rule, not to this theme.
- **The substitution variables**, declared across four `kubernetes_secret`
  resources in OpenTofu and mirrored in an eleven-line
  `tests/flux-substitutions.env`. Thematically identical, but the mirror exists
  so the fixture can be read without an estate, which is a reason a duplicate
  earns its place. Revisit if the two drift.
- **The control-plane listener table** added with #498. Two places by design:
  one declares what the machines should open, the other dials it. Collapsing
  them would leave the test asserting the declaration against itself.

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

### The address carries site, zone and host, and nothing else

**Chose:** `10.<site>.<zone>.<host>`, with the trust zone in the third octet.
**Rejected:** the hypervisor in the third octet, and the environment anywhere in
an address or a machine name.
**Because:** the question that settles it is not "what would be useful to read
off an address" - almost anything would - but **which dimensions carry a
boundary something can enforce.** A subnet is a real boundary: a NetworkPolicy
selects on it, a firewall rule references it, a route aggregates it. A label is
not. So a dimension earns a place in the address when it is enforceable, and
lives in configuration or in Kubernetes when it is not.

| Octet | Carries        | Values                                                         |
| ----- | -------------- | -------------------------------------------------------------- |
| 1st   | private prefix | `10`, fixed                                                    |
| 2nd   | **site**       | recorded per site as `octet`, e.g. `10`, `20`                  |
| 3rd   | **trust zone** | `0` infra · `10` trusted nodes · `20` LB pool · `30` untrusted |
| 4th   | **host**       | `100`+ control planes, `200`+ workers                          |

```text
site0-cp-100    10.10.10.100   vm 10100   trusted, control plane
site0-wk-200    10.10.10.200   vm 10200   trusted, worker
site0-dmz-100   10.10.30.100   vm 10300   untrusted, off the overlay
```

The VM id's hundreds digit mirrors the zone, so a stray id is legible on sight.

#### The leading octet cannot move

Recorded because "shift everything left to free a field" is the obvious idea and
it fails immediately. RFC 1918 offers exactly three private ranges -
`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16` - and only the first has spare
octets. The `10.` is not a wasted field, it is the ticket to using the space at
all.

The concrete failure is worth keeping: make the first octet the site number and
site 1 becomes `1.x.x.x`, which is APNIC space, and `1.1.1.1` is the resolver
this estate hands to every node in `compute.tf`. Site 1 would collide with the
estate's own DNS on its first packet.

`172.16/12` gives four usable bits in the second octet and `192.168/16` gives two
octets in total, so `10/8` with 24 free bits is already the most generous private
space available. Sub-dividing on non-octet boundaries would buy more dimensions
and is rejected for the reason the scheme exists: `10.10.10.100` is readable at a
glance and `10.10.148.100` is not.

#### Where the scheme runs out, and which wall arrives first

Asked directly: a very large machine appears in the closet and is carved into a
truly large pool of guests - does the addressing constrain it? Two walls, both
at about 254, and only one of them is the address scheme.

**Wall one: a zone is a /24, so 254 hosts.** This is cheap to lift and the
scheme was already spaced for it. Zone numbers step by ten - `0`, `10`, `20`,
`30` - which was deliberate: a zone owns a **decade of third octets**, not one.
So the trusted-node zone is `10.<site>.10.0` through `10.<site>.19.255`, which
is 2,540 addresses, and the readable property survives because the third
octet's tens digit is still the zone. The site's /16 is 65,534 addresses and
four zones currently occupy four of 256 third-octet values, so the space is
about 98% unused. There is a great deal of room and no redesign needed to reach
it.

Routing is unaffected: the overlay advertises the whole `/16` per site, so
internal subdivision never has to be CIDR-aligned for reachability. It has to
be expressible in a NetworkPolicy, which at worst means a zone listing several
CIDRs.

**Wall two: the pod CIDR, and this is the one that actually binds.** Talos
defaults the pod network to `10.244.0.0/16` and hands each node a `/24`, so the
cluster caps at **256 nodes** regardless of how much node address space exists.
Confirmed on the live cluster, which shows `PodCIDR: 10.244.1.0/24`.

**Neither the pod nor the service CIDR is declared anywhere.** `talos.tf` sets
no `clusterNetwork` fields at all, so the estate's single largest address
commitment is an undeclared upstream default. That is worth fixing on its own
merits - a value nobody wrote down cannot be reviewed, and it is invisible to
the addressing scheme this decision defines.

**And changing it requires a cluster rebuild**, because it is fixed in the Talos
cluster configuration at creation. So the moment to widen it is the rebuild
epoch 03 already requires for Cilium, not afterwards - the estate's own rule
that a rebuild should carry only what genuinely requires one cuts both ways,
and this genuinely does.

**One hazard to carry into that change.** Cilium's default cluster pool is
`10.0.0.0/8`, which contains **every site subnet this scheme defines**. Adopting
Cilium without setting the pod CIDR explicitly would collide the pod network
with the node network on day one. The existing note above - stay below
`10.96.0.0` because Kubernetes puts services at `10.96.0.0/12` and pods at
`10.244.0.0/16` - is the same caution one CNI earlier, and it does not cover
this.

**Wall three, which is not really a wall: VM ids.** The five-digit form
`<site octet><zone><host>` allows 100 hosts per zone. Six digits allow 1,000,
and Proxmox permits ids far beyond that, so this follows the fourth octet
rather than constraining it.

**The honest framing, though.** A machine of that size is not carved into 250
guests - it becomes a handful of very large nodes, because the entire point of
the orchestrator is that workloads are pods rather than machines. So the
constraint that binds at scale is pods per cluster, not nodes per subnet, which
is precisely the one that is currently undeclared and needs a rebuild to
change. The node addressing has an order of magnitude of headroom behind a mask
change; the pod network has none behind anything cheap.

#### Site belongs in the name and in the address, from one source

The site appears twice - as `<site>-cp-100` and as the second octet - and that is
not duplication to remove. **Names do not route.** Three things need the address
specifically: the overlay advertises one route per site, which requires
contiguous space; two sites must not collide, which was this epoch's original
driver; and forwarding happens on the destination IP, so whatever resolved the
name already needed it.

It is safe duplication because both derive from **one** config entry -
`sites.<site>.{name, octet}` - so the two cannot drift independently. That is the
condition under which restating a value is acceptable anywhere in this
repository.

#### Why not the hypervisor in the third octet

It reads well and costs three things, in increasing order.

**The load-balancer pool loses its home**, and it is needed this epoch -
`variables.tf` reserves `10.<site>.20.0/24` for it and the epoch 03 workloads
need service addresses from it.

**One cluster would span two subnets on one wire.** Both hypervisors' guests sit
on the same bridge and are L2-adjacent; putting them in separate /24s invents a
routing requirement that does not physically exist. This epoch already rejected
it above: nodes in one Proxmox cluster must share a subnet.

**It fights epoch 05's stated goal.** That epoch exists so that changing a node -
"its image, its size, the hypervisor it sits on, or the fact that it died" - is
ordinary. If the address encodes the hypervisor then moving a guest between boxes
**renumbers it**, and a Talos control-plane node's address is load-bearing in
five places: the machine config, the certificate SANs, the etcd peer URLs, the
talosconfig endpoints, and Flannel's `public-ip` annotation. A live migration
would become a rebuild.

**The want behind it is legitimate and is met in the fourth octet.** Banding host
octets by hypervisor - `100-119` for the first box's control planes, `120-139`
for the second's, `200-219` and `220-239` for their workers - makes placement
readable at a glance as a **convention rather than a contract**, so a guest that
later moves keeps its identity. Legible without being load-bearing.

And the hazard that actually bites when a hypervisor is added is placement rather
than addressing: `vm_placement` recomputes `i % length(hypervisors)` and re-deals
a running etcd member. The fix is the one this record already prescribes -
placement recorded rather than recomputed - and it is the same principle as the
site octet being a recorded field rather than a derived index.

#### Why the environment appears nowhere

There are three environments - management, production and staging - and none of
them earns an address or a machine name.

**They cannot be three clusters.** That is nine etcd members, and the capacity
section above establishes 24 GiB free. So they are namespaces in one cluster,
which answers the open question [`03-workload.md`](03-workload.md) records.

**A namespace does not own machines.** A worker runs whatever the scheduler puts
on it, so there is no such thing as a production VM unless nodes are deliberately
partitioned by environment - which is the walls-not-rules error the priority model
above rejects, and it strands capacity in whichever environment is idle.

**And an environment carries no enforceable guarantee.** `prod` and `stage` say
nothing about containment: the game server is production and its staging
counterpart is equally untrusted. The dimension that carries a security property
is the trust zone, which is why that is what the third octet holds.

If an environment ever does need real isolation, it is not a new dimension - **it
is another site**, and the second octet already expresses that.

**`staging` keeps its name.** `develop` was considered and rejected: the
promotion path in `deploy-infrastructure.yml` already encodes `main` to staging
and `v*` to production; `staging` is a term of art twice over, both the software
term and the construction term for where materials are gathered before use; and
`dev` collides with the name of the privileged user account, in a repository
where the privilege boundary is the thing most important to read correctly.

### The first split is by scope: estate, site, node

Decided 2026-09-25, after a site `demolish` deleted the WARP enrollment
application for the whole Cloudflare account and the next build failed looking
for it (#531). The application had been adopted into the site's state, so the
site's teardown destroyed it. That is a scope error, not a teardown bug, and it
re-plans this epoch's split: **the first cut is by scope, and the
infrastructure/platform cut below happens inside a site.**

#### Three scopes, and an operation at one cannot harm a wider one

| Scope      | What it is                                             | What it owns                                                                     | Program      |
| ---------- | ------------------------------------------------------ | -------------------------------------------------------------------------------- | ------------ |
| **estate** | the Cloudflare account and its Zero Trust organisation | the enrollment application, who may enroll (the Access policy), the split tunnel | `lawyer`     |
| **site**   | one cluster                                            | its machines, control plane, database, buckets, and its own tunnel and routes    | `contractor` |
| **node**   | raw compute, ready to be adopted by a site             | nothing                                                                          | -            |

"The organisation" and "the estate" are the same thing here: the account _is_
the estate, so the boundary is "the estate's, not the site's".

**Nodes are owned, not owners.** They are the site's keyed resources, in the
site's root and the site's state, and no node has a root or a verb of its own.
What "nothing done to a node may harm its site" asks for is already the
disposable-VM rule: a node is replaced, never repaired, and it holds nothing
the site needs back.

**A site's buckets are the site's**, even though they deliberately outlive a
rebuild of its machines. That is a statement about lifetime, not scope, and it
is not evidence that they belong to the estate.

#### The boundary is state, not a list of things to spare

The estate has its own OpenTofu root, `management/estate/`, with its own state
in its own R2 bucket (S3 backend, `use_lockfile`, encrypted with
`TF_ENCRYPTION` like every other state). A site's plan has no handle on any
estate object, so a site's destroy cannot reach one. The alternative, keeping
the objects in site state and having each teardown forget them first, is the
careful-handling fix: it works until somebody adds an object and not the
matching forget.

`tests/go/repo/estate_scope_test.go` holds the boundary in both directions.
It walks every `.tf` file in the repository and refuses:

- an estate type owned (by `resource` or `import`) anywhere but the estate
  root;
- any type in the estate root not declared as the estate's.

A new estate object is therefore a decision written into that test, never an
accident. The mutation ledger proves both directions.

What both roots need, they read from a file rather than from each other:
`management/tunnel-routes.json` is read by the site root, for the routes
through its own tunnel, and by the estate root, for the account's split tunnel.

#### The lawyer holds the estate's credentials, and only those

"Contractors don't build estates - lawyers do." The estate's verbs belong to a
second program, `scripts/lawyer`, and the name is a credential boundary as much
as a theme. In the user's words: "the lawyer holds the estate credentials and
estate credentials only. The contractor holds the site credentials and the site
credentials only."

| Verb              | Program    | Refuses when                                                      |
| ----------------- | ---------- | ----------------------------------------------------------------- |
| `build-estate`    | lawyer     | the estate's state already holds anything                         |
| `converge-estate` | lawyer     | the estate's state is empty                                       |
| `demolish-estate` | lawyer     | any tunnel stands in the account, i.e. any site, or no `-confirm` |
| `build-site`      | contractor | (renamed from `break-ground`, #534)                               |
| `converge-site`   | contractor | (renamed from `converge`)                                         |
| `demolish-site`   | contractor | (renamed from `demolish`)                                         |

Build and converge each refuse the other's case, so a converge can never
quietly build an estate from nothing.

**One vault per scope, named for the scope.** The estate's secrets are in a
1Password vault called `estate`, and each site's will be in a vault called by
its key (`site0`, `site1`). A service account token is granted per vault, so
the vault is the unit a program's reach can be drawn around. With one shared
vault, "only those" would hold only because each program renders its own
template, since either token could still read the other's fields. An umbrella
vault such as `homelab` groups exactly the things that must not share a token,
and repeating the scope inside the path (`op://homelab/site0/...`) says what the
vault should already say. So references read `op://estate/access/api_token`
and `op://site0/hypervisor/token_id`. The names are generic, so they sit in git
without costing forkability.

#### Four kinds of vault, and who may do what in each

Settled 2026-09-25. `-shared` means one thing everywhere: **the lawyer writes it,
a narrower scope reads it.**

| Vault           | Holds                                                                                                                                                            | lawyer       | site0        |
| --------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------ | ------------ |
| `estate`        | account-wide credentials: Access, the tailnet admin, R2 admin, the estate's state                                                                                | read + write | none         |
| `estate-shared` | what every site needs and nobody is harmed by: org name, account id, repo URL, the backup recipient (a public key), the alerting webhook, the foreman key (#539) | read + write | read         |
| `site0-shared`  | what the lawyer grants site0 alone: its tunnel's run token, its buckets' keys, its tailnet client                                                                | read + write | read         |
| `site0`         | what site0 generates or holds for itself: the hypervisor token, database, workloads                                                                              | **none**     | read + write |

Read-only is what stops a site harming its siblings through a shared vault:
site0 cannot overwrite a value site1 depends on, and 1Password enforces that
rather than this code. The lawyer never reads a site's own vault, and a site
never reads the estate's, so neither side needs an exception.

The lawyer lists the vaults its token can see and **refuses one that is neither
`estate` nor a `-shared` vault**. The contractor will do the same for its site:
its own vault and the two it reads, and nothing else.

#### The lawyer grants each site its plot

Three credentials a site uses today can harm another site, because the vendor
cannot scope them to one:

- **the tunnel API token**: anything that can create site0's tunnel can
  delete site1's;
- **the object storage admin token**: it reaches every bucket in the account,
  state backups included;
- **the tailnet**: every site mints keys with the same `tag:homelab-router`, and
  the policy auto-approves any route in `10.0.0.0/8` for that tag, so a leaked
  site0 client can advertise site1's `/16` and take its traffic.

So the lawyer creates what only an account-wide credential can create, and grants
the site a credential narrowed to it. That means the tunnel and its run token,
which serves that tunnel alone; the site's buckets and keys scoped to them; and a
tailnet OAuth client that can mint keys only for `tag:site0-router` and
`tag:site0-node`. The lawyer also owns the **tailnet policy**, which
`overlay-network.tf` already declined to manage because "every site deployment
clobbers the policy every other site depends on", and which was a manual console
step in `docs/tailnet-setup.md`. Generated from the site list, the policy
approves each site's router for that site's own `/16` only. That is the "precise"
option the setup doc called a chore; generating it removes the chore.

This is the vending pattern organisation-scale estates use, such as a landing
zone handing workload accounts narrow roles. Most homelab GitOps repositories
instead feed one vault through one token to the cluster, which is simpler and
hands a compromised cluster everything. The concrete threat here is the
self-hosted runner inside the cluster, which runs CI jobs beside a game server.
Today a site converge renders the account-wide R2 admin token onto it, so one
bad job could empty every bucket, backups included.

#### Where 1Password stops, and OpenBao takes over

A 1Password service account's vault grants cannot be changed after it is
created. Adding site1 would mean new `site1` and `site1-shared` vaults, and a new
lawyer service account to reach `site1-shared`: a rotation of everything the
lawyer holds. That is the right price for one site and the wrong one for many.
**OpenBao arrives before site1**, and per-path policies with dynamic
credentials are the ecosystem's answer to exactly this. So the vaults are built
for site0 alone, and the second site is OpenBao's to make cheap.

#### The order

1. #538: the estate root, the lawyer, and its vault check.
2. The site's vaults: `op://site0/...` and `op://site0-shared/...`, one
   template per site, and `homelab` retired.
3. The grants: the lawyer creates each site's tunnel, buckets and tailnet
   client, and owns the tailnet policy.

All three land before the rebuild.

The estate vault holds:

| Item     | Fields                                                                  | Notes                                                                                                                                                                                                    |
| -------- | ----------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `access` | `provider`, `account_id`, `api_token`, `members`                        | who may enroll and what an enrolled device reaches; the token may edit Access applications and policies, device settings, and read tunnels; `members` moved here from the site config's `tunnel.members` |
| `state`  | `bucket`, `access_key_id`, `secret_access_key`, `encryption_passphrase` | the passphrase is generated by the first build                                                                                                                                                           |

Two facts now exist in both vaults: the account id, and the fact that the
account is Cloudflare's. That is the price of a vault boundary, since a program
that cannot read the other vault cannot share a field with it.

What each program shares in code comes from `scripts/details`: the 1Password
wrapper, the password generator, the console, the state-encryption block and a
Cloudflare API client all moved there so the lawyer did not grow a copy of any
of them. Writing the console's first test found a live bug: the phase banner's
closing bar printed colour codes into pipes (#537).

#### Consequences for the site

- The site's `tunnel.api_token` now needs Cloudflare Tunnel edit and nothing
  else. It used to administer Access too, because the site owned the enrollment
  application. Narrowing it is what makes the boundary hold at the vendor as
  well as in the code.
- `tunnel.members` left the site's config, its Go type, its fixtures and its
  precondition. Who may enroll is checked where it is declared, in the estate.
- An estate-only change no longer counts as site work in the deploy workflow.

#### Found on the way, and not fixed here

- **Nothing converges the estate on merge** (#535). The site converge runs on a
  runner inside a site, which is the wrong place for the estate's token and
  does not exist while no site stands. So the estate's lane needs a
  GitHub-hosted runner, and `op` delivered to it. Until then an estate change
  is converged by hand with `task converge-estate`.
- **The tunnel routes collide as soon as there are two sites** (#536). The game
  server's route is a fixed `clusterIP`. Every cluster has the same service
  range, every site's routes share the account's one virtual network, and the
  split tunnel is one list for the account. This is harmless with one site and
  decided before the second.
- **The estate has no plan-on-pull-request.** A reviewer sees the estate's
  change as a diff, not as a plan. That belongs with #535, since a check must
  run the same operation as the action.

### Inside a site, the split is two roots sharing one config, and the seam is two values

The design for the provider split named as a constraint above, and the second
cut: it divides one site's root, after the scope split has taken the estate's
objects out of it. Written before any code moved, because the state migration is the expensive half to get wrong
and it is an operation only `dev` can run.

**Re-measured 2026-09-21.** Every count below is from the tree rather than from
the earlier description of it.

#### What is actually on each side

Eighteen resources move. Everything else stays.

| Layer            | Files                                                                                                                                             | Providers                             |
| ---------------- | ------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------- |
| `infrastructure` | `compute.tf`, `pools.tf`, `talos.tf`, `overlay-network.tf`, `object-storage.tf`, `cilium.tf`, `registry.tf`, and the site's tunnel in `tunnel.tf` | proxmox, talos, tailscale, cloudflare |
| `platform`       | `database.tf`, `gitops.tf`, `monitoring.tf`, `runner.tf`, `workloads.tf`, and the Kubernetes half of `tunnel.tf`                                  | kubernetes                            |

The eighteen, by kind, because this list is the migration inventory and a
resource missing from it is a resource the platform root would propose to
create a second time:

| Kind                   | Count | Addresses                                                                                                                                                                                                                          |
| ---------------------- | ----- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `kubernetes_namespace` | 7     | `.database`, `.flux_system`, `.monitoring`, `.valheim`, `.runner_system`, `.runners`, `.tunnel`                                                                                                                                    |
| `kubernetes_secret`    | 10    | `.state_db_credentials`, `.object_storage_credentials`, `.cluster_vars`, `.alerting_webhook`, `.monitoring_vars`, `.valheim_server`, `.runner_app_credentials`, `.runner_app_credentials_runners`, `.runner_vars`, `.tunnel_token` |
| `terraform_data`       | 1     | `.flux_bootstrap_apply`                                                                                                                                                                                                            |

#### Cilium is the last step of infrastructure, not the first of platform

`cilium.tf` looks like it belongs with the Kubernetes work and does not. It
declares no `kubernetes` resource at all - it is a `terraform_data` that writes
`talos_cluster_kubeconfig.this.kubeconfig_raw` to a temporary file and shells
out, precisely because it has to run before any node is Ready and therefore
before a `kubernetes` provider could connect to anything.

That makes the boundary mean something rather than being a filing decision.
**The infrastructure root's postcondition is "the nodes are Ready", which is
exactly the platform root's precondition.** A layer that ended one step earlier
would hand over a cluster whose API server answers and whose nodes do not
schedule, and the first `kubernetes_namespace` would be the thing that
discovered it.

#### The seam is two values, and that is the finding

The reason this is worth doing now rather than fearing: the platform files were
read for every reference that crosses the boundary, and there are only two.

Everything else the platform layer needs comes from `local.*` - and
`local.config` is `jsondecode(file(var.config_path))`. The rendered config is a
**file**, not a resource, so both roots read it directly and derive the same
locals from it. `variables.tf` is shared verbatim rather than plumbed through as
module inputs.

So the entire cross-boundary surface is:

| Value            | Produced by                                       | Consumed by                                                          |
| ---------------- | ------------------------------------------------- | -------------------------------------------------------------------- |
| the kubeconfig   | `talos_cluster_kubeconfig.this`                   | the `kubernetes` provider, and `terraform_data.flux_bootstrap_apply` |
| the tunnel token | `cloudflare_zero_trust_tunnel_cloudflared.estate` | `kubernetes_secret.tunnel_token`                                     |

Both are secrets, so both outputs are `sensitive`. Both land in the
infrastructure root's state, which is already encrypted with OpenTofu state
encryption keyed from the vault - so this adds a value to a ciphertext blob
rather than adding a place a credential sits in the clear. Worth stating rather
than assuming, because "an output is fine, state is encrypted" is the kind of
claim that is true here and not true of a fork that has not set `TF_ENCRYPTION`.

The third thing that looks like a dependency is not one. Every platform
resource carries `depends_on = [data.talos_cluster_health.this]`, and there is
no cross-root equivalent of that edge. There does not need to be: the health
gate becomes **structural**. The platform root runs only after the
infrastructure root's apply returned zero, and that apply contains the health
data source. The ordering stops being an edge inside a graph and becomes the
order two applies happen in, which is the whole point of the split.

`tests/go/repo/kubernetes_gate_test.go` refuses a `kubernetes_*` resource with
no such edge, so that guard has to move with the resources rather than be
deleted - it becomes "the platform root runs after infrastructure", asserted
against the phase sequence rather than against a `depends_on`.

#### Two roots, two schemas, and the reason it is not two workspaces

The backend is `pg`, `schema_name = "management_cluster_state"`, in
`backend_pg.tf.disabled` - disabled because ignition starts with a local state
file and `migrate` moves it into the cluster afterwards.

Each root gets its own schema: `management_infrastructure_state` and
`management_platform_state`. Not two workspaces in one schema, for a reason that
is the whole argument of this section restated: workspaces share a
configuration, so both roots would have to declare both provider sets and the
`kubernetes` provider would be unresolvable again for any infrastructure
operation. Separate schemas are separate configurations, which is what is
wanted.

The infrastructure root keeps the existing schema name rather than taking a new
one, so the larger half of the state is not migrated at all - see below.

#### The migration is a contractor verb, not a runbook

Claude cannot perform any of this. The state is encrypted from the vault and
Claude holds no vault credential, so the migration is `dev`'s to run - and the
estate's rule is that a procedure a person performs by hand is not a procedure.
Either the tool does it or it does not.

So: `contractor split-state -site <site>`, once, idempotent, refusing to start
unless it can finish. Three properties it needs, each from a rule this estate
has already paid for:

1. **The larger half does not move.** The infrastructure root adopts the
   existing schema, so every VM, pool, Talos secret and Cloudflare object stays
   exactly where it is. Only the eighteen listed above are touched. A migration that rewrites the whole state to move a quarter of it
   is a migration with a much worse failure mode than it needs.
2. **It either completes or refuses to start.** "TNT is TNT. Unexploded
   ordinance is not safe." The preconditions - both schemas reachable, the
   eighteen addresses present in the source state and absent from the target,
   the rendered config valid - are checked before the first write, not
   discovered partway.
3. **It is resumable in the only direction that matters.** The mechanism is
   `state rm` from the source and `import` into the target, per resource. Doing
   the import first and the removal second means an interruption leaves a
   resource in **both** states, which is recoverable by re-running. The other
   order leaves it in neither, which is a resource nothing tracks - the exact
   thing `teardown.go` is written to avoid.

Every one of the eighteen is cheaply importable, which is what makes this
tractable: a namespace's id is its name, a secret's is `namespace/name`. There
is one exception and it needs deciding rather than discovering.
`terraform_data.flux_bootstrap_apply` has no import identity - it is a
provisioner, and its `triggers_replace` is the only record that it ran. Moving
it means it re-runs on the platform root's first apply. **That is acceptable and
should be stated in the verb's own output**: `flux bootstrap` against a cluster
that already has Flux is idempotent, and the alternative - hand-writing a
`terraform_data` instance into a state file - is worse than re-running an
idempotent command.

#### What each phase becomes

The prize is `cluster.go`. Today it is five hand-sequenced `-target` applies,
every one of them a workaround for the coupling this split removes - including
one whose only job is to materialise the kubeconfig so that `tofu import` can
configure providers at all, and a comment explaining that the bucket adopt must
happen after it for that reason.

| Phase       | Today                                                | After                                                                     |
| ----------- | ---------------------------------------------------- | ------------------------------------------------------------------------- |
| `compute`   | targeted applies in the one root                     | untargeted apply of the infrastructure root                               |
| `cluster`   | five targeted applies, ending with an untargeted one | splits: the rest of infrastructure, then one untargeted platform apply    |
| `health`    | reads `data.talos_cluster_health` in the one root    | unchanged, in infrastructure - and now genuinely gates the handover       |
| `take-over` | one backend                                          | both, and refuses if the two disagree about which estate they hold        |
| `plan`      | one plan                                             | two plans, reported as one answer                                         |
| `migrate`   | moves one local state into one schema                | two, and it is the phase `split-state` supersedes for new sites           |
| `backup`    | encrypts one state                                   | encrypts both - and the state backup's own integrity test has to see both |

`adoptOrphanedR2Bucket` moves to the infrastructure root and loses its ordering
comment entirely: with no `kubernetes` provider in that root, `tofu import` has
nothing unresolvable to trip over, which is the concrete form of the payoff.

#### The cost, stated rather than waved at

- **Two applies and a handoff**, which the record already accepted above.
- **A second backend schema to create**, and `backup` covering both. A backup
  that silently covers one of two states is worse than the single state it
  replaced.
- **`plan` answers twice.** A converge that changes only a workload secret
  produces a no-op infrastructure plan beside a real platform one. That is an
  improvement in signal and a change in what a reader expects.
- **The fixture corpus and the contract tests read `management/cluster/<name>`**
  by path - `phases/contract_test.go` and `tfsource.Read` both do - so the
  split touches the guards as well as the code. That is the corpus problem from
  the driver above arriving on schedule, and it is the argument for doing the
  fixture work first rather than after.
- **`environments/*/infrastructure/` and `modules/infrastructure/` still do not
  exist.** This split is the prerequisite the record names, not the module
  carving itself. It does not on its own advance the epoch's acceptance test,
  which remains "adding a site requires no commit".

#### Why this is not the module carving, and must still come first

Worth being blunt, because two pieces of work that both produce directories
under `management/` are easy to conflate. This split creates two roots out of
one. The epoch's acceptance test needs a **site module** instantiated once per
discovered site, and a config discovered from the vault rather than declared in
`config/management.tpl.json`.

The order is forced rather than chosen: carving modules out of a root whose
provider configuration depends on a resource inside it bakes that dependency
into the module boundaries, and every future site inherits it. That is the
sentence the design constraint above ends on, and it is the reason this is step
one.

### Object storage: the bucket is the unit, and what belongs at each scope

Agreed 2026-09-21, after the operator asked what belongs at the estate, site and
node levels and how to display it. The answer came out of one constraint rather
than out of taste, which is why it is short.

#### The constraint that decides it

**R2 API tokens scope per bucket.** There is no prefix or directory condition on
a permanent token, and a token permitted to write is also permitted to delete.
The second half was established in #94, and it is the reason token scoping
cannot make a backup writer unable to destroy backups.

So a bucket is **exactly one blast radius**, and a prefix inside a bucket is
filing rather than isolation. One rule follows, and every decision below is an
application of it:

> Two things belong in different buckets if and only if one of them being
> compromised must not be able to destroy the other. Everything finer is a
> prefix.

#### What each level of the hierarchy maps to

| Level      | Maps to                       | Why                                                                                                                                                                             |
| ---------- | ----------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Estate** | the vendor **account**        | R2 has no layer above a bucket, and `CLAUDE.md` already counts the object storage account among the things an estate shares. A second estate is a second account, not a prefix. |
| **Site**   | a **bucket** boundary, always | "A site is an isolation boundary." Two sites must not be able to destroy each other's data. Already true - the base bucket name is a per-site vault value.                      |
| **Node**   | **nothing**                   | "Node: shares everything with its site." A node is disposable and its data belongs to its site.                                                                                 |

The node row is the one worth stating rather than leaving implied. **If anything
is ever node-scoped in object storage, that is the defect** - it means a
disposable thing has acquired durable state of its own, and the next rebuild of
that node is going to find out.

**Environment is a fourth axis and it applies to workloads only.** `CLAUDE.md`
is explicit that the management tier has no staging or production form: one
cluster hosts both overlays, separated by namespace. So staging and production
are a property of a _workload's data_ and of nothing else. A platform bucket
that grew an environment suffix would be asserting a second platform that does
not exist.

#### The layout

One set per site - so the estate holds N sets for N sites - named for the **site**,
because the site is the isolation
boundary and every site in an estate shares one storage account:

| Bucket              | Holds                                       | Credential held by                   | Survives a teardown |
| ------------------- | ------------------------------------------- | ------------------------------------ | ------------------- |
| `<site>`            | the database's WAL archive and base backups | CloudNativePG, in-cluster, permanent | no                  |
| `<site>-state`      | age-encrypted OpenTofu state dumps          | contractor, transient                | **yes**             |
| `<site>-staging`    | staging workload data                       | staging workloads                    | **yes**             |
| `<site>-production` | production workload data                    | production workloads                 | **yes**             |

`<site>` is `local.site_name` - the slug of the site's vault name, already
computed and already naming every VM, so a site's buckets read `<site>-state`
beside machines called `<site>-cp-100`. Nothing new was built for this; the
value existed.

**The estate is not in the name.** Every bucket in the account belongs to the
estate, so a prefix saying so distinguishes nothing. The operator's objection,
and it is the general rule here: a descriptor carrying no information is not a
name, it is noise.

**Real names are used, because R2 is private.** The line this repository draws
is not "never write a real name" - it is obfuscate in public, where a real name
is a dumb secret, and use real names where the tree is private and a reader
benefits from recognising what they are looking at. The slug reaches R2 and the
rendered config, and never git.

A first attempt used the `sites{}` map key instead, on the grounds that a map
key is unique by construction and therefore makes collision unrepresentable.
That is true and it was the wrong trade: it buys a guard nobody needed at the
cost of a bucket tree nobody can read. The slug needs a uniqueness assertion
instead, which is the next section.

That gives a clean statement of the teardown rule, which used to be a special
case and is now the general one: **the estate's own working data is destroyed
with the estate; everything that exists to outlive it is forgotten before the
destroy and adopted back by the next ignition.**

#### The slug now needs asserting, and did not before

`local.site_name` derives from a free-form vault field, so two sites can be
given names that collapse to one slug. Nothing asserted that, on either side -
octets were asserted unique and slugs were not.

**That was harmless until now, which is why it survived.** Two sites are two
Proxmox clusters, so two machines called `<site>-cp-100` never met. It stops
being harmless the moment the slug names buckets, because every site in an
estate shares **one** R2 account - so two sites with the same slug do not
collide noisily, they silently write into each other's state dumps and each
other's workload data.

Asserted on the slug rather than the raw name, which is the whole point:
"North Street Office" and "north-street-office " are two names and one bucket. It is asserted in
`registry.tf` and in `config.go`, the same both-sides shape the octet check
already has, and `config.SiteSlug` is now exported so the check and the name
actually used cannot drift apart - they were the same expression inline, which
is not the same thing as being one expression.

This is the general lesson worth keeping: **a value's uniqueness requirement
comes from what consumes it, not from what produces it.** The slug did not
change; what reads it did, and that is what made an assertion necessary.

#### Two things this fixes rather than tidies

**The state dumps stop sharing a credential with the database they rescue.**
These are the estate's two recovery layers and they are deliberately two: the
database backups restore into a running cluster, and the state dump is what
rebuilds the cluster that database lives in. While both sat in one bucket they
shared one credential, and that credential is the one sitting permanently in a
cluster Secret. One compromise took both layers of a design whose entire point
was having two.

**It closes half of #94.** That issue's second remedy is "the bucket should not
be something the automation can remove." `demolish` empties the bucket it is
about to delete, because Cloudflare refuses to delete a non-empty one - so the
operation most likely to precede needing a state dump was the operation that
destroyed every state dump. Eleven objects went that way. The `-state` bucket is
released before the emptying, exactly as the workload bucket already was.

The pre-teardown warning changed with it, and that is not cosmetic. It used to
say "THESE ARE THE AGE-ENCRYPTED STATE BACKUPS, and they are the only copies",
which is now false, and an operator who believes their backups are about to be
destroyed makes worse decisions in the five minutes before a demolish. It now
names the survivors as well as the casualty.

#### `-workloads` is retired, and the timing is the argument

The bucket created by #330 is replaced by `-staging` and `-production`. R2 cannot
rename a bucket, so this is a destroy and a create rather than a move - safe
only because **nothing has ever written to it**. The world backup meant to fill
it does not exist (#372), which makes this the cheapest moment the change will
ever have.

If that turns out to be wrong the failure is the safe one: Cloudflare refuses to
delete a bucket holding objects, so the apply stops and says so.

#### Declared twice, on purpose

The set lives in `object-storage.tf`, which creates the buckets, and in
`scripts/contractor/config/buckets.go`, which decides each one's fate
at teardown. Neither can be derived from the other - OpenTofu cannot read a Go
table, and the teardown is a Go program running when no OpenTofu is loaded - so
this is the same defence in depth the config contract already uses.

`TestTheBucketTableAgreesWithTheHCL` refuses drift in either direction, and the
dangerous direction is the quiet one: **a bucket added to the HCL but not the
table is created, filled, and then destroyed by the next teardown with a zero
exit code**, because nothing told Sterilize to release it. The reverse is noisy
and harmless.

The address and fate used to be string literals repeated at each call site - two
copies at two buckets, which would have been eight at four. That is not a set,
it is eight chances to disagree.

**Named resources rather than one `for_each`.** The set is small and changes rarely,
and a `for_each` keys every address by a map key, which
`TestResourceAddressKeysUseAPlaceholder` refuses - for_each keys normally come
from the config, so a name in an address is a vault value published in a plan.
These keys would have been literals, but a guard that has to tell those two
apart is a guard with an exception in it, and the rule here is to move the work
to where the guard already looks rather than widen the guard. Named resources
also hand `tofu state rm` an address with no quoting in it, and that is the
command deciding whether a bucket survives a teardown.

#### Still open

- **Per-bucket credentials do not exist yet.** The estate holds one R2 key
  scoped to _all_ buckets, so the separation above is structural rather than
  enforced until #481 and #483 land. Both need an operator: a token is created
  in a vendor console and its halves go in the vault.
- **The database bucket is not yet named for its site.** It is the one bucket
  still taking `object_storage.bucket` from the config, and it cannot move in
  place: OpenTofu would have to destroy and recreate it, and Cloudflare refuses
  to delete a bucket holding objects - which this one does, continuously.
  Changing it also moves CloudNativePG's `destinationPath`, starting a fresh
  WAL archive with no base backup behind it. A rebuild recreates the bucket
  anyway, so the rename is free then and impossible now. The
  `object_storage.bucket` config key goes with it, and so does
  `Bucket.FromConfiguredName`.

  The plan for this change proves the mechanism is safe: `moved` renamed the
  resource from `cloudflare_r2_bucket.homelab` to `.database` and the bucket
  did not appear in the plan at all. A failed move would have read
  `destroy .homelab` + `add .database`, which is the WAL archive destroyed.

- **Nothing enumerates the account.** Two buckets existed that this repository
  never created and nothing noticed; the operator found and deleted them. A
  table in a record drifts - the guard that would not is one that lists the
  account's buckets and fails on any the repository does not declare, in the
  survey rather than in a hermetic test, since it needs a credential.

## Outcome

## Deferred

### Where the worker pool got to, and what is left

Written mid-flight rather than at close, because the ordering below is the part
that would be expensive to reconstruct.

**Landed or in flight.** Two workers at 6 vCPU and 8 GiB (#244), and CI pointed
at them with requests and a required anti-control-plane affinity (#245). The
count was briefly three, for a database that then did not move - the retraction
and its trigger are recorded beside the count in `compute.tf`.

**The order the rest has to happen in**, and each entry says what blocks it:

1. ~~**Move the operators off the control plane and give them requests**~~
   (#237) - **done**, together with what was step 3, because they are one
   decision rather than two: requests set the QoS class the kubelet evicts by,
   the priority class sets the order the scheduler admits and preempts by, and
   a required affinity is only safe when the thing it constrains can preempt
   its way to a slot. What each pinned chart was read for is recorded below.
2. ~~**Taint the control planes.**~~ - **moved to epoch 03**, 2026-09-07. The
   reasoning is below, under "The taint belongs to whoever owns the storage".
3. ~~**Three `PriorityClass` objects**~~ - **done**, in the same change as
   step 1. Values are local rather than the built-in `system-cluster-critical`
   the table above names; the reason is in `priority-classes.yaml`.
4. **Proxmox pools by role**, as its own change - it edits every existing VM,
   and the converges that create machines should not also be the ones that
   modify them.
5. **A second runner scale set**, so `interactive` and `batch` CI have separate
   `maxRunners` interlocks and a lint job cannot queue behind an integration
   run for a slot. This entry was missing from this list until 2026-09-07 while
   being present in "What this epoch therefore builds" above - the list said
   four things where the plan said five, and a work order that disagrees with
   itself is how a step gets skipped without anybody deciding to skip it.

### The taint belongs to whoever owns the storage

Step 2 is moved to epoch 03 rather than deferred inside this one, because the
thing blocking it is not scheduling and never was.

Two blockers were recorded against it and only one survives. The OpenEBS helper
pod was retracted above: it builds its own tolerations from the taints on the
node it is provisioning for, so it follows a volume onto a tainted control plane
with nothing set. What remains is the state database. `tofu-state-1`, `-2` and
`-3` sit on Local PV Hostpath, which pins each volume to the node whose
directory holds it. Tainting the control planes has CloudNativePG try to
reschedule the database onto workers, and **the data does not follow it**.

So the taint cannot land until there is an answer to "where does stateful data
live when a machine it is on becomes ineligible", and that question is epoch
03's by the existing division of labour - `03-workload.md` already records the
same trap for a Valheim world. Keeping the taint here would have epoch 02 close
on a step whose real prerequisite is owned by another epoch, which is the shape
that produces a step nobody does.

What epoch 02 keeps is the part that needed no answer: a **required**
anti-control-plane affinity on everything that can move, which is already
landed. That gets the same placement outcome for those workloads without
touching the ones with state underneath them. The taint's remaining value is
that it also covers anything added later without an affinity - real, and worth
having, and not worth blocking an epoch on.

### Pools edit no machine at all, and the role could not have been granted

**Step 4 above is done**, and its wording is left standing on purpose: striking
it through would edit the lines main's step 5 was inserted against, and git
cannot separate a modification from an adjacent insertion. That is #328 in
miniature - the entry stays as written and this section carries the status,
which costs a reader one hop and costs nobody a merge conflict. Strike it
when nothing else is in flight.

Step 4 was written as "it edits every existing VM, and the converges that
create machines should not also be the ones that modify them". That is true of
the obvious implementation and false of the one that shipped, and the
difference is worth keeping because it is the second time in this epoch that
reading the provider rather than assuming changed the answer.

**`pool_id` on the VM resource was the wrong mechanism, twice over.** Reading
bpg/proxmox v0.111.1 rather than the documentation page: the attribute is not
`ForceNew`, so it is an in-place move via the pools API rather than a rebuild -
that part was better than feared. But it carries a `DiffSuppressFunc` that
ignores the change whenever the new value is empty and the old one is not. So a
machine can be put into a pool declaratively and **never taken out of one**;
deleting the line from the config does nothing at all, silently. A change that
cannot be reverted through the config is not something to do to five machines
on a whim.

`proxmox_pool_membership` is a first-class resource instead. Removing it
destroys the membership, which is what a reader of the diff expects. And
because membership lives outside the VM resource, **no machine is touched**:
the plan is creates of a new kind of object beside them, and the three
control planes holding etcd get no diff whatsoever. The caution in step 4 was
aimed at a risk this implementation does not have.

**The blocker was elsewhere, and it was not small.** The `TerraformProv` role
carries no `Pool.*` privilege, so the converge would have failed with a 403.
Adding `Pool.Allocate` and `Pool.Audit` to `pve_role_privs` fixes that for a
future host and **would have changed nothing on this one**, because the
playbook deliberately had no "modify existing role" task: it initialises a host
from zero, and its comment said privilege changes are picked up by a rebuild.

The rebuild is deferred until there is a second hypervisor. So "picked up by a
rebuild" meant "not until then", and the only routes left were running `pveum`
by hand - ClickOps, which this estate refuses - or rebuilding the machine the
estate runs on. **A rule that blocks the only path to compliance is not a rule,
it is an outage**, and it is the same shape as the push guard that blocked the
one person who could not use the alternative.

The playbook now reconciles the role to exactly what the file declares. That
widens its job from "initialise" to "initialise and keep true", deliberately
and in both directions: a privilege added here reaches the host on the next
run, and a privilege added by hand on the host is removed on the next run. The
second half is the point rather than a side effect.

**Grouped by function, and by nothing else.** Not by hypervisor - the console
already groups by node and a second copy of that answer adds nothing. Not by
lifecycle stage - the management tier has no staging or production form, and
borrowing those words from the workload tier is a mistake `CLAUDE.md` names
outright. Function is also the only axis that survives the estate changing: a
machine can move hypervisor, change size or be rebuilt on a new image and
remain a control plane throughout.

Pool ids carry the site name because pools are datacenter-scoped. Two sites
sharing a Proxmox cluster would collide on a bare `control-plane`, and Proxmox
answers a collision by **adopting the other estate's machines into this pool**
rather than by failing. The config supports several sites by design, so that is
expressible rather than theoretical.

The guard is `TestEveryMachineClassIsAssignedAPool`, and it is pointed at the
direction this actually breaks. Nobody will delete the pools - that is a
visible, deliberate change. What happens is a new class of machine gets written
beside the others and nothing says where it belongs, so the tree grows an
ungrouped machine and the grouping quietly stops being true of everything. The
test reads the VM resources out of `compute.tf` and requires each one's
`for_each` collection to be placed, so the new class is a red build rather than
a gap nobody can see.

### What reading the charts actually found

Recorded because the instruction was to read each pinned chart's own
`values.yaml` before trusting a path, and doing it turned up four things that
guessing would have got wrong. The general lesson is the one the OpenEBS
manifest already stated for itself: **a Helm value at a path the chart does not
read is accepted in silence** - no error, no warning, no event - so the manifest
says the pod is configured and the pod is not.

**Flux was never BestEffort, and #237 never said it was.** The generated
`gotk-components.yaml` already gives all four controllers requests and limits.
Placement was the only thing missing, so Flux is patched by the overlay's
kustomize `patches` rather than by values, alongside the image digests and for
the same reason: `flux bootstrap` rewrites that file wholesale.

**Three of Flux's four controllers carry `system-cluster-critical`;
notification-controller carries nothing.** That is upstream being consistent -
it is the one controller whose death does not stop reconciliation - but
unclassified is priority zero, which puts the component that _reports_ failures
below every class this estate declares, lowest exactly when something is going
wrong. It is patched into `critical`. The other three are left on upstream's.

**`listenerTemplate` is not a strategic merge.** The chart copies it verbatim
into the `AutoscalingRunnerSet`, and the ARC controller merges it into the pod
it builds with hand-written field assignments
(`mergeListenerPodWithTemplate`). Most pod-level fields are **assigned
unconditionally**, whether or not the template sets them, so supplying a
template silently resets anything it omits - `terminationGracePeriodSeconds`
would have dropped from the controller's 60 to Kubernetes' 30 with nothing
reporting a change. It is restated in the manifest for that reason.
`nodeSelector` is the one field guarded by a nil check, and it replaces rather
than merges, so setting one there would discard the controller's own
`kubernetes.io/os: linux`.

**Retraction: the OpenEBS helper pod is not a blocker for the taint.** This
record briefly said it was the worst of them, and #269 was filed on that
reading. Both were wrong, and the reasoning error is the part worth keeping.

The provisioner does not create volume directories itself - it launches a
short-lived helper pod on whichever node the volume belongs to. The chart
exposes only `image`, `hostNetwork` and `timeoutSecs` under `helperPod`: no
tolerations, no `nodeSelector`. That was read as "the helper cannot follow a
volume onto a tainted control plane", which does not follow. **The absence of a
configuration knob is not the absence of the behaviour.**

Reading the program the chart deploys settles it. `provisioner-localpv` v4.6.0
reads the taints off the `Node` it is provisioning for and builds a matching
toleration for each one - `GetTaints(selectedNode)` into `selectedNodeTaints`
into `WithTolerationsForTaints`, which emits `Operator: Exists` for a taint
with no value, and `node-role.kubernetes.io/control-plane:NoSchedule` carries
none. So the helper tolerates whatever the node it is aimed at happens to
carry, automatically. That is better than a setting, because there is nothing
to remember to set.

**The taint's only remaining blocker is the one already recorded**: the
CloudNativePG instance pods need a toleration on the `Cluster` spec, which is
the same edit as their priority, and is why the priority was deferred to step 2.

Two general lessons, both cheap and both nearly skipped. `values.yaml` says
what is _configurable_, not what _happens_ - so follow a missing option into the
source before concluding the capability is missing. And check the newest
release before treating a limitation as real: here it made no difference, since
4.6.0 is the latest chart, but establishing that is the first question rather
than one option among four.

**The state database's priority is deferred to step 2 deliberately.** Adding
`priorityClassName` to a CloudNativePG `Cluster` changes the instance pod spec,
which CloudNativePG answers with a rolling restart and a switchover of the
database holding this estate's OpenTofu state. The taint needs a toleration on
that same spec, so doing both at once costs one rolling restart instead of two.
Until then the database is unclassified, and the risk of that is nil rather
than small: it is alone on the control planes with nothing to be preempted by.

**Three of these pins carry a Renovate annotation for a Renovate that does not
run** (#270). Noticed while fetching the charts: `cloudnative-pg` is six minor
versions behind, and a Flux `HelmRelease` chart version is the one pin here
that nothing updates and nothing reports on. Not dangerous - the operator is
not in the data path and the estate is empty - but an annotation naming a tool
that is not installed reads exactly like a pin somebody is maintaining.

**Required placement on Flux is safe at ignition, and this was checked rather
than assumed.** `gitops.tf` bootstraps Flux behind
`data.talos_cluster_health.this`, which lists `worker_nodes` and depends on the
worker configuration apply, so workers are Ready before Flux is installed.
Recovery from losing every worker does not need Flux either: workers are built
by OpenTofu, which does not depend on Flux in either direction.

**What stays on the control plane, and why it is not a compromise.**

The **state database** stays. It is to the estate what etcd is to the cluster:
both are the record of desired state, and nobody proposes moving etcd to a
worker. Two things make it concrete rather than aesthetic. `converge` blocks on
the database before it can do anything, so a database on the machine class
epoch 05 exists to destroy routinely means the repair tool depends on the thing
being repaired - and `worker_count` is a config value, so one character could
destroy every instance in parallel. R2 makes that survivable rather than
terminal, which is why this is a trade rather than a rule; the reason to
decline it is that the prize is 256Mi of requests.

**CoreDNS** stays too, and it is Talos-managed, so moving it is a Talos-level
decision rather than a manifest edit.

That leaves "lean" meaning **the control plane carries only control-plane
work**, which is the property that protects quorum. The RAM number is a
second-order optimisation and should not be taken before epoch 04 measures
anything - see the gotcha below about what changing it would do.

## Known driver: the first placement audit, and what it found

Run against the live cluster on 2026-09-06, immediately after #271 merged. The
audit itself was a throwaway command; its assertions are now
`tests/go/integration/placement_test.go`, so the next one is a nightly run
rather than a session.

**#237 delivered.** All eight operators are on workers - CloudNativePG, three
Flux controllers and the runner's ARC controller, listener and OpenEBS
provisioner between the two of them. What remains on the control planes is
Talos's own machinery and the three state-database instances, which is exactly
the intended shape.

### The prize was not the memory, and this record said it was

Worth retracting precisely, because the reasoning was wrong in kind rather than
in degree.

Moving the operators off the control planes was described here as recovering
capacity, with a note that the prize was small - a couple of hundred mebibytes.
**It recovered none at all.** Those five pods were `BestEffort`; a `BestEffort`
pod reserves nothing, so removing it frees nothing the scheduler was holding.
The arithmetic is exact: a control plane now requests 1138 MiB, and every byte
of it is Talos's - 512 for the API server, 256 for the controller manager, 64
for the scheduler, 50 for the CNI, zero for kube-proxy - plus 256 for the
database instance. The node carrying both CoreDNS replicas requests 140 more.

What the move actually bought is different and better: **the machines holding
quorum no longer host any cgroup the OOM controller will choose first, and no
longer host workloads that can grow without a reservation.** The defect was
never a shortage; it was that the estate's own control machinery sat in the
eviction path of a build. Say that, rather than talking about megabytes.

### Both CoreDNS replicas are on one node

The clearest defect the audit found, and nothing in the repository could have
shown it.

Cluster DNS runs two replicas so that losing one machine does not take DNS with
it. Both are on the same control plane, and have been since the cluster was
built. Talos does ask for them to be spread - the CoreDNS Deployment it renders
carries a `podAntiAffinity` at
`preferredDuringSchedulingIgnoredDuringExecution`, weight 100, over
`kubernetes.io/hostname` (read from `k8stemplates/coredns.go` at v1.13.8, not
assumed).

**`preferred` is the whole story.** The scheduler will co-locate when it has a
reason to, and `IgnoredDuringExecution` means it never revisits the choice. A
pair placed together during bootstrap - when one node was Ready and there was
no alternative - stays together for the life of the cluster, with nothing
anywhere reporting it. That is the failure shape worth carrying forward: **a
soft constraint is evaluated once, at the least representative moment there is,
and then remembered forever.**

Restarting the Deployment would spread them today and would not stop it
recurring. This was first written as an integration test, and that was wrong:
nothing in this repository asks for the spread, so a red run would report
something no commit could fix. **It is an alert, and it moved to epoch 04.**
The ecosystem's answer to "pods that are in the wrong place because scheduling
happened at a bad moment" is the descheduler, and that is what to evaluate
rather than anything bespoke. Tracked as #274.

### kube-proxy is BestEffort, is not ours, and is about to be deleted

Issue #237 deferred this with "may not be ours to set; worth confirming rather than
assuming". Confirmed, in both directions.

It is genuinely not ours: Talos's `cluster.proxy` machine-config surface
exposes `disabled`, `image`, `mode` and `extraArgs` and nothing else - there is
no `resources` field, and `ProxyConfig` in `v1alpha1_proxyconfig.go` has no
other accessor. Editing the DaemonSet directly would be reconciled back.

It is also less alarming than it looks, for a reason worth stating because it
separates two mechanisms this epoch has been treating as one. kube-proxy is
`BestEffort` **and** `system-cluster-critical`. So it is protected from the
scheduler, which will never preempt it, and unprotected from the kubelet, which
ranks eviction by QoS class and will pick it first under node memory pressure.
**Priority and QoS are different guards against different actors**, and a pod
can have one without the other.

The disposition is to leave it. [`03-workload.md`](03-workload.md) already plans
`cluster.proxy.disabled: true`, because Cilium replaces kube-proxy - so this is
a component on its way out, and building a workaround for it would be work
thrown away. The integration check exempts it by name with that reason attached,
rather than skipping `kube-system` wholesale, which would hide a Talos
regression as readily as it hides this.

### The numbers, for epoch 04 to argue with

|                | control plane (x3)           | worker (x2)          |
| -------------- | ---------------------------- | -------------------- |
| Allocatable    | 3.95 cpu, 3281 MiB           | 5.95 cpu, 7435 MiB   |
| Requested      | 0.46-0.66 cpu, 1138-1278 MiB | 0.3-0.4 cpu, 370 MiB |
| Of allocatable | 12-17% cpu, 35-39% memory    | 5-7% cpu, 5% memory  |

Two things follow. **Allocatable is not the guest's RAM** - Talos keeps about
815 MiB of a 4 GiB machine and 757 MiB of an 8 GiB one - so every headroom
figure in this record that divided by the guest total was optimistic.

And **the workers are nearly empty**: 370 MiB reserved of 7435. Two runners at
2 GiB each would take them to about 60%, which is the first honest answer this
epoch has to the goal of filling the box on purpose. The room for epoch 03's
workloads is there and is larger than expected.

### What the audit could not answer

Requests, not usage. This is the admission-control view - what the scheduler has
promised - and it says nothing about what is actually resident. The two diverge
in both directions: `kube-proxy` requests zero and uses something, and the API
server reserves 512 MiB whether or not it wants it. Closing that gap needs
metrics, which is epoch 04, and no amount of care with `kubectl get` substitutes
for it.

### Asserted or inherited: what reading three charts for their security context found

The instruction from #237 - read the pinned chart's own `values.yaml` before
trusting a path - was applied again for #272, and turned up three things that
the issue itself had got wrong. Recorded because two of them are corrections to
a written claim, and a wrong claim in a record is worse than no claim.

**CloudNativePG 0.23.0 was not "partial".** Its chart already ships
`runAsNonRoot: true`, `allowPrivilegeEscalation: false`,
`readOnlyRootFilesystem: true`, uid/gid 10001, RuntimeDefault seccomp and
`drop: [ALL]` as defaults. #272 read the values file's layout rather than its
values. The manifest now restates them anyway, and the reason is worth keeping
separate from the reason for the other two: not to harden anything, but so a
chart upgrade that quietly dropped a default produces a visible disagreement
instead of a silent downgrade in a version bump that looks like every other one.

**The OpenEBS provisioner runs as root**, which #272 asserted was true of
nothing here. `docker.io/openebs/provisioner-localpv:4.6.0` declares no `USER`,
so it is uid 0. Asserting `runAsNonRoot` there would not harden it, it would
stop the component that hands out the volumes the state database sits on. Filed
as #315 with the experiment that would settle whether it needs uid 0.

**`localpv.securityContext` is read by no template in the subchart.** It is in
`values.yaml` and appears nowhere in `templates/`. This is the third time in
this epoch the same trap has been found - a value at a path nothing reads is
accepted in silence - and the first time it has been found in a path that a
written issue was already recommending. The general form is worth stating
plainly: **a knob existing in `values.yaml` is not evidence that the chart reads
it.** The only proof is the template that consumes it.

The guard is `TestEveryWorkloadRequiresItsSecurityPropertiesRatherThanInheritingThem`
in `tests/go/repo/workload_placement_test.go`, sharing the table that already
records which chart version each path was checked against. A chart bump reopens
the question rather than carrying the old answer forward, and a workload that
asserts nothing must say in prose why - because an omission and a considered
exemption are otherwise the same silence.

### The talosconfig named one endpoint and no nodes

The complaint in #235 was that every node-targeted `talosctl` command refused
on first use, because the Talos provider leaves `nodes` empty and nothing set
it. Fixing it
turned up a second fault in the same three lines: `endpoints` was
`[local.node_ips[0]]`, a single machine. Every control plane proxies the Talos
API, so naming one bought nothing and lost the credential precisely when that
machine was the one being diagnosed - which is the case a diagnostic exists for.

Both are now set to `local.node_ips`. Workers are deliberately excluded from
`nodes`: the commands this credential exists for are the quorum ones, and those
are meaningless on a worker, so including them would make every `talosctl etcd`
call answer three times and error twice.

It is guarded twice, on purpose and at different moments.
`writeTalosconfigTo` re-reads what OpenTofu rendered and refuses to hand over a
talosconfig naming no nodes, which catches a provider that stopped honouring the
argument; `TestTheRenderedTalosconfigNamesNodesAndEveryEndpoint` catches the
branch that removed it, which is cheaper by a whole incident. The static one
asserts only that `nodes` is set to _something_ - pinning the expression would
be a change detector that fails on every rename and passes while the behaviour
rots.

**Still open: the kubeconfig has the same shape and a harder fix.**
`cluster_endpoint` is `https://${local.node_ips[0]}:6443`, baked into the
machine configuration, so a rendered kubeconfig points at one control plane too.
Unlike the talosconfig this cannot be fixed by setting another argument - it
needs a virtual IP or a load balancer in front of the API, and the address band
at `.20.0/24` was reserved for exactly that. Filed as #316 rather than fixed here.

## Gotchas

### Do not taint the control planes in the change that adds workers

The instinct once workers exist is to taint the control planes so nothing lands
there again, and it has to wait.

`tofu-state-1`, `-2` and `-3` sit on OpenEBS Local PV Hostpath, which pins each
volume to the node its directory was created on. Tainting the control planes
would have CloudNativePG try to reschedule the state database onto workers, and
the data would not follow it. That is the same trap
[`03-workload.md`](03-workload.md) already records for a Valheim world, arriving
earlier and against the database that holds the estate's own OpenTofu state.

So the safe first increment is a `nodeSelector` on the runner scale set: CI goes
to the workers, nothing else reschedules, and nothing moves that has state
underneath it. Tainting becomes a considered follow-up once the storage question
has an answer, and that answer belongs to epoch 03.

### Changing control-plane memory would restart all three at once

Recorded before anybody tries it, because the obvious way to slim the control
planes is a converge and the obvious way is wrong.

`memory.dedicated` is not hot-pluggable here - the guest agent is disabled and
there is no balloon device - so a change requires the machine to restart. The
three control-plane VMs have no dependency on one another and OpenTofu's
default parallelism is ten, so a converge that changed memory on all three
would restart all three at roughly the same time. That is a quorum loss on a
cluster that was healthy a moment earlier.

Doing it safely is one machine at a time, waiting for health in between, which
is exactly the machinery epoch 05 exists to build. Until then a control-plane
resize is a deliberate, supervised operation rather than a config change, and
it is worth confirming against a real plan before believing this note.

### A version pin in a tool's own environment namespace is an instruction

The nightly Integration Tests failed for five nights and the cause was a name.

`.github/actions/versions` exports every key in `scripts/versions.env` into the
job environment, which is how a lane gets a pinned version without restating
one. rclone configures **every one of its flags** from a matching
`RCLONE_<FLAG>` environment variable. So `RCLONE_VERSION=1.75.0` was never
delivered to rclone as a pin - it arrived as `--version=1.75.0`, and `--version`
is a boolean:

```text
CRITICAL: Invalid value when setting --version from environment variable
RCLONE_VERSION="1.75.0": invalid argument "1.75.0" for "--version" flag:
strconv.ParseBool: parsing "1.75.0": invalid syntax
```

Every rclone call in the job then exits 1 before doing any work. The Backup
phase halted on its first upload, so no encrypted state backup was written; and
because the `Integration tests` step runs after it, the estate's only check
against a real cluster did not run either. Two things stopped, one of them
silently, and the run reported the loud one. `backup` is in `ConvergePhases`
too, so a merge-driven deploy reaches the same call with the same environment.

The fix is the name: the pin is `RCLONE_DEB_VERSION` now, which is inert
because rclone has no `--deb-version` and ignores an `RCLONE_` variable matching
no flag. `tests/go/repo/versions_test.go` refuses anything else in the
namespace, and the mutation ledger proves that refusal fires.

#### The part worth remembering is that this was found once already

The runner image hit exactly this in its build, because docker puts a build
argument into the environment of the `RUN` that uses it. It was closed there by
aliasing the `ARG` to `RCLONE_DEB_VERSION` and mapping `RCLONE_VERSION` onto it
in the workflow - a fix that worked, was written up carefully in a comment, and
left the trap fully armed everywhere else. The alias treated a name that is
dangerous in any environment as a Dockerfile problem.

That is the same shape as the push-guard and ruleset pair recorded elsewhere:
**when one rule is enforced in more than one place, correcting one of them is
not correcting the rule.** The question to ask on finding a collision like this
is not "where did it bite" but "what else hands this name to the same tool" -
and the answer here was nine workflows, sitting in the file the first fix
deliberately did not touch.

The generalisation is cheap to apply: a version pin is data about a tool, and
it must not be spelled the way that tool reads configuration.

### The default that prints was the one nobody wrote down

The integration tier published a cluster-admin kubeconfig into a public Actions
log — CA, client certificate and `client-key-data` — once per test that touched
the cluster, and the state database's connection string with its password
beside it (#491).

Nobody chose to print either. `harness.TofuOptions` did not set a `Logger`, and
terratest's default logger prints the output of every command it runs. The
outputs of this OpenTofu root are the estate: `kubeconfig`, `state_conn_str`,
addresses. So the credential was not leaked by a line of code that handled it
carelessly — it was leaked by a line of code that was never written.

That is the lesson worth keeping. The code review question "is this value
handled safely here" had a correct answer everywhere it was asked. The question
that was never asked is what the library does when you say nothing.

#### It had been learned once already, one package away

`run.TofuApply` moved the contractor to a `-json` summary after a converge
published a site name from a resource description into a public log. The
reasoning is written out at length in `scripts/contractor/internal/run/exec.go`,
and it is exactly this reasoning. It was applied to the contractor, which is not
where the tests build their options.

This is the second entry in this record with that shape, after the rclone pin:
**when one rule is enforced in more than one place, correcting one of them is
not correcting the rule.** Both times the first fix was careful, well
documented, and left an identical trap armed somewhere the author was not
looking. The habit that would have caught both is to finish a fix by asking who
else does this, rather than by writing up why the fix is right.

The remedy is at the mechanism: `TofuOptions` sets `Logger: logger.Discard`, so
every caller and every future output is covered, and
`TestTerratestNeverPrintsEstateOutputs` refuses both an options struct built
outside the harness and a harness that stops discarding.

#### A client certificate is not a password

Worth stating plainly because it changes what "fix the leak" costs. Kubernetes
has no CRL and no OCSP: the API server trusts any certificate its CA signed
until that certificate expires. The leaked one had about a year to run. So the
only true revocation is rotating the cluster CA, and
`docs/state-and-secret-rotation.md` is honest that for this estate
`contractor demolish` plus a fresh ignition is the better-tested path.

The connection string was the cheaper half: a password can simply be changed.

### Two tests asked a real cluster for opposite things

`TestDeployedWorkloadsAreOffTheControlPlane` reported three node-exporter pods
as trespassers on the control planes. `TestOnlyTheNodeExporterOfTheStackRunsOnAControlPlane`
asserted, in the same run, that one had better be there — a node's memory
headroom is what epoch 04 watches, and a control plane nobody measures is the
tightest machine in the estate.

No cluster can satisfy both. The tier failed on an estate behaving exactly as
designed, which is the worst kind of red: it costs the time of whoever reads it
and it teaches them that the tier is unreliable.

The cause was two independent spellings of "is this the node exporter", written
months apart, neither aware of the other. There is one now, `isNodeExporter`,
used by both. The general form: when two tests make claims about the same
subject, the predicate that identifies that subject belongs in one place, or
they will eventually disagree and the estate will be blamed for it.

### Applying a machine configuration is not the same as the machine running it

`talos_machine_configuration_apply` records the configuration it **sent**. It
does not read back what the machine is doing. So a node that accepted a change
and never acted on it is, in OpenTofu state, indistinguishable from one that
did — and `tofu plan` reports no drift, correctly, about an estate that is not
what the repository says it is.

That is how `listen-metrics-urls = "http://0.0.0.0:2381"` sat declared and
inert. The repo tier proved the declaration was present. The deployed tier
proved Prometheus was unhappy. Nobody owned the question in between — _is the
machine running what we declared_ — so the only way to answer it was a person
opening a terminal and running `talosctl`.

**That is the defect, not the etcd listener.** A property this estate declares
must not need a human to confirm, and "run this command and tell me what it
says" is the shape of an answer the estate should already have. The operator's
rule covers it exactly: either the automation works or it does not, and we do
not give ourselves a shortcut.

#### Dial the port, do not read the config back

The tempting fix is to read each node's running machine configuration over the
Talos API and compare it to the declaration. That tests the spelling. A config
can contain the right key while the service has not restarted to act on it,
which is the failure being guarded — so the comparison would pass on exactly
the estate that is broken.

Dialling the port tests the thing. It is also credential-free: the tier already
reaches these node addresses for the Talos API check, so it does not acquire
Talos API access, which sits below Kubernetes and can reset a machine. A
diagnostic that widens the tier's blast radius to confirm a metrics port would
be a poor trade.

`TestEveryControlPlaneOpensTheListenersItWasConfiguredFor` dials all three
listeners on every control plane, and reads etcd's metrics — deliberately
unauthenticated, which is why it is a separate listener from the client port —
to prove the socket is etcd rather than merely open.

#### And the guard that stops it going stale

A table of three ports is a fixed list, and a fixed list silently stops
covering. `TestEveryDeclaredControlPlaneListenerIsDialled` reads the
`extraArgs` patches out of `talos.tf`, finds every component told to listen
somewhere other than loopback, and fails in both directions: a listener
declared and not dialled, or dialled and not declared.

Matched on the component rather than the port, because the port is a component
default that `talos.tf` never states. What the configuration does say is which
component is being moved off loopback, and that is the fact both sides have to
agree about.

The general form, which is the third time this epoch has produced one: the
repository declaring something is not evidence the estate has it. Wherever a
declaration has an observable effect, something automated has to observe it.

### `tofu state list` fails on the one run where empty is the answer

The first real `lawyer build-estate` stopped at Take over with "No state file
was found". It asked for the estate's resources with `tofu state list`, and
`list` treats "there is no state yet" as an error. A first build is exactly the
run where no state is the correct and expected answer. `tofu state pull` prints
nothing and exits 0 in the same situation, which is why the contractor already
used it, and the lawyer does now. It also parses what it pulls: state that is
present but unreadable is an error, never an empty estate, because build-estate
would take an empty estate as permission to build over it.

The unit tests had covered which verb admits which state, but not the command
that produced the state they judged. It took the first run against a real
backend to find it.

### The pre-merge check and the post-merge action were different commands

Renaming one R2 bucket resource stopped every converge on `main`.

`moved` blocks and `-target` are mutually exclusive. OpenTofu resolves a rename
while it builds the plan and refuses to build one that would record only half of
it, so if either endpoint falls outside the targets it stops with **"Moved
resource instances excluded by targeting"**. Every apply in the converge before
the Cluster phase is targeted, so a single `moved` block halted the converge at
its _first_ apply — the disk image — and nothing after it ran. Not the VMs, not
the machine configuration, and not the untargeted apply at the end of Cluster
that would have settled the move and created the new buckets.

The estate then reported two unrelated-looking symptoms for a day: the nightly
backup failing with `NoSuchBucket` for buckets the repository described and
nobody had created, and Alertmanager paging every four hours about an etcd
metrics listener that a machine configuration nothing was applying had never
opened.

#### Why nothing caught it

This is the part worth keeping. `contractor plan` — the check that runs on the
pull request — issues **one untargeted plan**. `contractor converge` — what runs
after the merge — issues **targeted applies**. Verified against OpenTofu 1.12.6
on a throwaway configuration: an untargeted plan of a pending move succeeds, and
a targeted plan of the same configuration fails.

So the pre-merge check was not weak, or badly written, or unlucky. It was
**structurally incapable** of failing the way the post-merge action would,
because it ran a different command. The pull request was green, and would be
green again.

The generalisation is the one worth carrying: **a check earns its place by
running the same shape of operation as the thing it is checking.** Wherever the
verification path and the action path diverge — different flags, different
targets, different verbs — the difference is the exact region no check covers,
and it will be discovered in production.

#### What was done, and what was only filed

The converge settles a pending rename before its first targeted apply:
`run.SettleMoves` runs `apply -refresh-only -auto-approve` targeted at the
move's own endpoints. Verified rather than assumed — refresh-only reports "0
added, 0 changed, 0 destroyed", so it cannot create or destroy anything, and
targeting it at the two addresses avoids the untargeted refresh that would read
`data.talos_cluster_health`, the read that sat for ninety minutes during a
teardown. `-refresh-only` with `-refresh=false` is refused outright, so that
combination is not available.

It is in Compute rather than take-over because take-over is shared with
`contractor plan`, which must change nothing, and settling a move writes state.

Two hermetic guards, so this needs no estate to stay honest:
`TestPendingMovesFindsEveryMovedBlockInTheEstate` counts the estate's own
`moved` blocks against what the parser returns, so a block written in a shape
the parser misses fails rather than being skipped; and
`TestComputeSettlesRenamesBeforeItApplies` asserts the ordering, because
settling after the first targeted apply is the same as not settling at all.

Making the plan path actually mirror the converge path is the larger fix and is
filed rather than bundled. The two sequences would have to share their declared
steps the way `TeardownSteps` already does, which is a change to the deploy
path and deserves its own review.
