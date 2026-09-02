# Epoch 03 — Workload

- **Tier / path:** `environments/`
- **Branch:** `epoch/03-workload`
- **PR:** —
- **Status:** Not started

## Goal

Stand up staging and production as thin pointer configs over the epoch-02
modules, and make the promotion path real: merge to `main` deploys staging, a
`v*` tag deploys production.

## Scope

In scope (per `README.md`):

- `environments/staging/{infrastructure,applications}/`
- `environments/production/{infrastructure,applications}/`
- Flux/Kustomize overlays per environment.

Explicitly out of scope:

- New module functionality — belongs in epoch 02.

Two workloads are named success criteria for this epoch. They are not examples:
if the estate cannot host these, the tier has not done its job.

- **A website**, reachable on the LAN and probably the WAN. HTTP, so it is the
  easy half and Cloudflare Tunnel can carry it.
- **A Valheim dedicated server**
  ([a guide to dedicated servers](https://www.valheimgame.com/support/a-guide-to-dedicated-servers/)).
  This one sets the constraints, because it needs inbound **UDP 2456-2458**.

### Cilium is still required, for one of the two reasons given

Worth stating precisely, because one of the two arguments for Cilium was
withdrawn and the other was nearly withdrawn with it.

**Withdrawn:** that Cilium is needed so pods can reach the tailnet. That rested
on a measurement taken while the hypervisor was off the tailnet, and nothing
about pod egress has actually been established. See the correction in
[`02-abstraction.md`](02-abstraction.md). It is not an argument for anything
until the test is re-run against a live peer.

**Standing, and untouched by that:** Flannel does not implement NetworkPolicy.
This is a property of Flannel rather than an observation about this estate -
it provides pod networking and no policy controller at all - so a
NetworkPolicy object here is accepted by the API server, stored, and never
evaluated by anything. A policy that appears to isolate the game server and
does not is worse than having none, because it is isolation somebody will
believe in.

That matters here specifically because the game server is the first workload
this estate will run that is genuinely untrusted. It takes inbound UDP from the
public internet through a port forward, by the decision below, and it shares a
cluster with the Kubernetes API and the state database. Isolating it is not a
nicety; it is the condition on it being allowed to run at all.

**So the sequencing changed, not the requirement.** Cilium was briefly thought
to be blocking epoch 01's converge, which would have made it urgent and would
have justified rebuilding the cluster to get it. It is not. It is a
prerequisite of this epoch, and it can be planned rather than rushed - but it
is still a prerequisite, and no amount of the other argument collapsing changes
that.

The part that makes it awkward is unchanged too: Cilium must be in place before
nodes go Ready, so it cannot arrive through Flux the way everything else does,
which means a cluster rebuild rather than a converge.

### The overlay grants everything to everyone, which is why enrolling players is not an option

Recorded here because it is the strongest argument for the port-forward
decision below, and because it is true of the estate **today** rather than only
in the hypothetical.

The tailnet's access policy is the default one:

```json
{ "src": ["*"], "dst": ["*:*"], "ip": ["*"] }
```

Every device on the overlay may reach every other device, on every port. There
is no rule distinguishing a hypervisor from a laptop, and none distinguishing
either from a games console. So a device on this tailnet can reach the Proxmox
API, the Kubernetes API, and the state database, because nothing says
otherwise.

**That is what settles the Zero Trust option for the game server.** Enrolling
every player was already rejected as too heavy an ask - installing a client to
join a game is a real cost - but the heavier objection is this one: under the
current policy, enrolling a player would give that player's machine the same
reach into the estate as the hypervisor has. The ask is not "install a VPN
client", it is "join a network where you can reach my database".

The policy could of course be narrowed, and would have to be before any human
outside the household joined. Worth stating plainly so the option is rejected
for the right reason: the objection is not that Zero Trust cannot express this,
it is that the estate has not expressed it, and the amount of policy work
required to make enrolment safe is larger than the port forward it would
replace.

**It also changes what a new device on the mesh means.** With an allow-all
policy there is no such thing as a device with limited reach, so any machine
appearing on the overlay - a cousin's computer, a rebuilt laptop, anything -
has full access from the moment it joins. `scripts/contractor/internal/survey` reports untagged
devices against a baseline for exactly this reason, and the reason it treats a
new one as worth stopping for rather than logging is that there is currently no
weaker position for such a device to be in.

Narrowing the policy is not this epoch's work, but it is a prerequisite of the
epoch that puts anything on the internet, and it belongs on the same list as
NetworkPolicy enforcement: both are the difference between isolation that is
believed and isolation that exists.

### Why the game server decides the network design

Cloudflare Tunnel's public hostname routing is HTTP and TCP; it cannot carry
arbitrary UDP, and public UDP is Spectrum, which is enterprise-priced. Cloudflare
Zero Trust _can_ carry UDP over WARP private networking, but every player would
have to enrol in the organisation and run the WARP client, which is a heavier ask
than it sounds and routes traffic through Cloudflare's edge rather than directly.
Tailscale would work and gives direct peer paths, at the cost of every player
installing it.

The decision is a **port forward**. It is the only option that asks players to
install nothing, and it is the first genuinely inbound path into this estate -
which is a materially different posture from anything built so far, and the
reason the isolation question below is not optional.

### The isolation this requires

The intent is that the game server is walled off from everything else, and
today that is not achievable. The cluster runs Flannel, which **does not enforce
NetworkPolicy**: a policy saying the game server may not reach the Kubernetes API
or the state database would apply cleanly, report no error, and do nothing. That
is worse than having no policy, because it looks like protection.

**The decision is to move to Cilium.** It gives NetworkPolicy that is actually
enforced, which is what makes "walled off" true rather than decorative.

It is not a swap. Talos ships Flannel, so moving means `cluster.network.cni.name:
none`, probably `cluster.proxy.disabled: true` since Cilium replaces kube-proxy,
and - the part that matters - installing Cilium _before nodes go Ready_. A node
without a CNI never reaches Ready, and Flux cannot schedule until nodes are
Ready, so Cilium cannot arrive the way OpenEBS and the runner do. It has to come
from Talos `inlineManifests` or be applied by OpenTofu, ahead of the Health gate.

That changes how the cluster comes into existence, which is why it needs its own
record before any code moves.

The payoff reaches past the game server: with enforced NetworkPolicy, the
hypervisor access recorded in `02-abstraction.md` can be narrowed to the runner
pod rather than every pod on the node subnet - which that record names Flannel
as the reason it could not be.

### Storage, which is the quieter problem

A Valheim world is state, and OpenEBS Local PV Hostpath pins a volume to one
node. When that node is replaced - which every image change does, since an image
change means a rebuild - the world goes with it. Whatever this epoch does about
workloads has to answer that before anyone plays on it.

## Open questions to settle first

- `deploy-infrastructure.yml` already encodes the promotion model: `main` ->
  `staging`, `v*` tags -> `production`. Confirm GitHub Environments of those
  exact names exist with the right protection rules before the first apply.
- Do staging and production share one cluster with separate namespaces, or
  separate clusters? This decides whether the Flux target paths diverge from
  epoch 01's `clusters/management`.
- Where does per-environment secret material come from? The `op inject`
  pattern from epoch 01 assumes a human at a terminal and does not transfer
  to Flux reconciliation. External Secrets or SOPS is the likely answer.

## Decisions

_Record as made._

## Outcome

## Deferred

## Gotchas
