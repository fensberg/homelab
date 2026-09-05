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

### Three lanes, sorted by what tolerates delay

The operator's second model, and it supersedes the heavy/light split this
section first proposed. That one sorted work by size on a security axis - jobs
with estate access against jobs without - and let latency ride along with it.
Sorting by **how much delay the work tolerates** is the property the scheduler
actually acts on, and it produces three lanes rather than two:

1. **Emergency vehicles.** Everything yields, including traffic already moving;
   pushing a car onto the shoulder is an acceptable price. etcd, the API server,
   CoreDNS, the state database, Flux, and the runner listener.
2. **Commuters.** In a hurry, and delay is the whole cost. The fast pull request
   lanes - lint, format, test - where the job itself is 40 seconds and a
   two-minute queue is the entire user-visible latency.
3. **Logistics.** Important, and slow by nature. Deliveries and refuse
   collection: integration runs, converges, image builds, state backups, the
   clerk's sweep, dependency updates. They must arrive; they need not arrive
   now.

The assignment is by tolerance, not by importance. A state backup is one of the
more important things the estate does and it belongs in lane 3, because nothing
observes its latency.

#### Lane 2 jumps the queue but must not run anyone off the road

The refinement that makes this implementable, and it is not obvious. Kubernetes
priority does two things at once - it decides who gets the next free slot, and
it decides who gets evicted to make one - and the three lanes want those
answers to differ.

A commuter should get the next gap ahead of a truck. A commuter should **not**
be able to evict a truck that is already twelve minutes into a thirteen-minute
run, which is exactly the trade the "eviction is not free" decision above
refuses. Preempting a long job to admit a 40-second one destroys twelve minutes
to save two.

`PriorityClass` separates them. `preemptionPolicy: Never` places a pod ahead of
lower-priority _pending_ pods while never evicting a _running_ one:

| Lane        | Priority                    | `preemptionPolicy`     | Yields to   |
| ----------- | --------------------------- | ---------------------- | ----------- |
| 1 Emergency | `system-cluster-critical`   | `PreemptLowerPriority` | nobody      |
| 2 Commuter  | mid                         | **`Never`**            | lane 1 only |
| 3 Logistics | low, and it may be negative | `Never`                | lanes 1, 2  |

So preemption exists in exactly one place - the emergency lane - which is the
smallest surface that still protects quorum, and matches this record's
preference for admission control over eviction everywhere else.

#### What makes it a lane is the rule, not a wall

Worth stating because the model invites the wrong reading. Road lanes are not
partitions: an empty lane can be driven in, and what makes it a lane is a rule
about yielding. Three dedicated node pools would be walls, and walls would strand
capacity in whichever lane happens to be idle - which is the opposite of the
goal that produced this whole section.

**One pool of workers, three priority classes.** The one genuine partition is
the one already designed: lane 1 lives on the control planes, lanes 2 and 3
share the workers, and the `nodeSelector` above is what draws it.

The security axis does not force a second partition either. Fork-run lanes must
not be able to _reach_ the estate, which is a NetworkPolicy question and
therefore waits on Cilium - see [`03-workload.md`](03-workload.md). It is not a
question about which node they sit on. So the earlier claim that two independent
arguments demanded the same structure was half right: they demand separate
**credentials and network policy**, and separately a lane assignment. Those are
different mechanisms and conflating them was the error in the first draft.

#### Deliveries and refuse are not quite the same lane

The operator's third lane names both, and they differ in one way worth keeping.
A delivery must eventually complete - somebody is waiting on that integration
run - so it wants bounded retries. Refuse collection is idempotent and
catches up: a missed backup or dependency sweep is repaired by the next
scheduled run doing the same work. The practical consequence is narrow but real:
a killed truck carrying refuse needs no retry at all, and one carrying a
delivery does.

#### The lanes govern infrastructure operations too, not only CI

Creating a virtual machine is **lane 3**. It must complete - a worker that never
appears is a failure - and nothing observes its latency, because nobody is
waiting on the second one. The same is true of a converge, a state backup and an
image build. That the model extends past CI jobs to the estate's own operations
is worth stating, because it is what makes it a scheduling policy for the estate
rather than a CI feature.

The consequence is a pleasing inversion: **lane 3 work is what builds the
capacity lanes 1 and 2 consume.** The slow lane lays the road.

#### Lane by lane, what is actually short

Measured rather than assumed, because the three lanes are not short in the same
way and the remedies are different.

**Lane 1 has capacity and no protection.** The control planes use about 1.2 GiB
of 3.8 GiB each, so the emergency lane is not short of room. What it lacks is
any claim on that room: `Taints: <none>`, no requests on the dispatcher or the
operators (#237), and no `PriorityClass`. Its capacity is real and entirely
unreserved, which means a heavy lane-3 job can take it and has. **The remedy is
not more capacity, it is making the capacity it already has non-negotiable** -
which is requests and priority, not machines.

**Lane 2 does not exist.** Nineteen jobs across the workflows run on
`ubuntu-latest` and five on the self-hosted scale set; every one of the eight
pull request validation lanes is in the first group. So the commuter lane has
no estate capacity at all today, and building it is a migration rather than a
resize - with the prerequisites [`01-ignition.md`](01-ignition.md) already
records, `harden-runner` not surviving the move being the substantive one.

**Lane 3 is the one genuinely starved.** It is the only lane running in the
estate today, and it is what #236 killed.

So the workers being built serve lanes 2 and 3, and the lane 1 work is a
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
re-materialise the image first. **It makes lane 3 work slower for no memory
back.** The right disposition is to leave it alone.

#### The model immediately finds a misassignment

Applied to what is running today, the runner listener is the **dispatcher** -
always on, tiny, and if it dies no CI runs at all - so it is unambiguously lane

1. It is currently `BestEffort`, along with the ARC controller, the
   CloudNativePG operator and the OpenEBS provisioner (#237). The eviction order in
   force right now puts the dispatcher in the ditch first.

That is the argument for #237 being a prerequisite rather than hygiene, in one
line: the lanes do not exist until every vehicle has been assigned to one, and
an unassigned vehicle is not in lane 3, it is on the hard shoulder waiting to be
hit.

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

Worth noting where that work would land in the lane model: a development
environment is interactive, so delay is its whole cost, which makes it **lane 2**
rather than a fourth lane of its own.

### Where the utilisation actually is

A correction to this section's own framing, made after the numbers closed. The
hypervisor is already about 60% committed on memory, so "the estate runs at a
few percent" is true of CPU and misleading about memory: 3.88% of 18 cores
against roughly two thirds of the RAM already spoken for.

That is not a coincidence, it is the one-fuse principle observed from the other
side. **The resource with no breaker is the one with all the headroom, and the
resource with the breaker is nearly committed.** So filling this box is
overwhelmingly a CPU exercise - overcommit vCPU, pack the lanes, let contention
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
   is reckless. It is also what assigns each vehicle to a lane, and an
   unassigned one is not in lane 3 - it is on the shoulder.
4. **Three `PriorityClass` objects**, per the lane table above, with
   `preemptionPolicy: Never` on lanes 2 and 3 so preemption exists only where it
   protects quorum.
5. **A second runner scale set**, so that a commuter lane and a logistics lane
   have separate `maxRunners` interlocks and a lint job cannot queue behind an
   integration run for a slot.

Then epoch 04 measures it, and epoch 05 makes it elastic if that is ever worth
anything - which [`05-node-lifecycle.md`](05-node-lifecycle.md) records that it
currently is not.

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
partitioned by environment - which is the walls-not-lanes error the lane model
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

1. **Move the operators off the control plane and give them requests** (#237).
   Flux's four controllers, ARC's controller and listener, the CloudNativePG
   operator, the OpenEBS provisioner. Blocked on reading each pinned chart's
   own `values.yaml` first: placement and resources are set through Helm values,
   and **a wrong values path is silently ignored rather than rejected**, which
   is the worst failure mode available - it looks applied and is not. The
   OpenEBS manifest already records this discipline for itself.
2. **Taint the control planes.** `allowSchedulingOnControlPlanes = false`, with
   tolerations for the two things that stay. It has to come after step 1, or
   the operators become unschedulable at the moment nothing can reconcile them
   back.
3. **Three `PriorityClass` objects**, per the lane table above, with
   `preemptionPolicy: Never` on lanes 2 and 3 so preemption exists only where
   it protects quorum.
4. **Proxmox pools by role**, as its own change - it edits every existing VM,
   and the converges that create machines should not also be the ones that
   modify them.

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
