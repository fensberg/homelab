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

### The untrusted zone is a node, the workload is a container, and the isolation is three layers

**Chose:** the game server runs as an ordinary pod, reconciled by Flux, on a
**dedicated Talos worker in the untrusted zone that does not join the overlay
network**.
**Rejected:** a pod on a shared worker with only NetworkPolicy; a plain virtual
machine running the game server directly; and an LXC container on the
hypervisor.
**Because:** this was derived by working backwards from what a compromise
reaches, which is the only way the layers can be justified individually.

#### What a compromised pod reaches today

Assume the process is taken. A game server accepting inbound UDP from strangers
is a live category, not a hypothetical.

- **The Kubernetes API**, by service address, from any pod.
- **The state database**, holding this estate's own OpenTofu state.
- **The hypervisor's API**, because the cluster reaches it over a flat network -
  recorded in [`02-abstraction.md`](02-abstraction.md).
- **The overlay network, which is the worst of the four.** Every node carries
  the tailscale extension from the single shared schematic, and the tailnet
  policy is the default `{"src": ["*"], "dst": ["*:*"]}`. A compromised pod on
  an overlay-joined node therefore reaches the hypervisor, the workstation and
  every other site.

The API server is the obvious worry and it is the second worst. Overlay
membership is the real exposure, because nothing narrows what the mesh grants.

#### Three layers, each answering a different question

**NetworkPolicy answers "what may it talk to."** Necessary, and insufficient
alone: it says nothing about a container escape, because the escape does not
traverse the network.

**A dedicated node answers "what shares its kernel."** This is the layer that
decides against a shared worker, and the reason is specific rather than
general - the thing it would share a kernel with is the CI runner, which holds
vault credentials and reaches the estate.

**Omitting the overlay answers "what does the machine itself reach."** This is
the layer that is not currently expressible, and it has a real cost: the
tailscale extension is in the one shared schematic, so an untrusted node
requires a **second Talos schematic without it** - a second image, a second
`proxmox_download_file`, and #97 applying to both. Recorded as a cost rather
than discovered later, because it is the least obvious consequence of the
decision and the most likely to be dropped for convenience.

#### Why not a plain virtual machine

Stronger on paper - no kubelet, no cluster credentials, not a member at all -
and it loses on everything else. Talos is a Kubernetes operating system and
cannot do it, so this means adding a **second operating system** to the estate
with its own image, patching and provisioning path. It also means managing the
machine by hand or by Ansible, which makes it a pet and puts an interactive
management path into the one machine that should be least reachable. The
estate's rule applies directly: either the automation works or it does not, and
a shortcut is not the answer.

#### What an escape actually obtains, which is the test that matters

The fail-closed principle is not "the attacker cannot get in", it is "what they
obtain is worthless". Audited against the chosen design, an escape onto the
untrusted node yields:

- **No shell, no SSH, no package manager.** Talos has none.
- **Kubelet credentials scoped by Node authorization and NodeRestriction**, so
  the node may read secrets of pods bound to it - which, with only untrusted
  workloads scheduled there, are that workload's own.
- **No etcd membership**, because workers are not members.
- **No overlay**, by the schematic.
- **A zone subnet with policy on it.**
- **The Talos API behind mTLS**, for which the container holds no certificate.

A machine that reaches nothing and can read its own secrets. That is the
property being bought, and each of the three layers above is load-bearing for
one line of it.

#### The dedication has to be enforced, not conventional

The audit holds only while the node runs untrusted workloads **and nothing
else**. If the scheduler places anything else there, the blast radius grows and
nothing announces it.

So it is a **taint with `NoSchedule`, and a toleration carried only by workloads
in the untrusted zone** - not a `nodeSelector` convention and not a note in this
record. This repository has already shipped one policy that applied cleanly and
enforced nothing; the distinction between isolation that exists and isolation
somebody believes in is the whole subject of this epoch.

#### Consequences for naming, and for capacity

The machine is `<site>-dmz-100` at `10.<site>.30.100`, per the addressing
decision in [`02-abstraction.md`](02-abstraction.md). **The workload gets no
machine name at all** - it is a namespace and a Deployment, named in Kubernetes.
That separation is why the environment never needed to appear in a VM name.

On capacity: a dedicated node wants roughly 4-6 GiB, and only about 8 GiB
remains after epoch 02's two workers. That is tight until the build VM's 16 GiB
returns, which is expected, and it sequences correctly - the workers are epoch
02 and this is epoch 03.

Two things this does **not** solve, both already named above. The world save is
on OpenEBS Local PV Hostpath and therefore pinned to a node - now a node
specifically chosen to be destroyable. And the tailnet's allow-all policy
remains the reason any new device on the mesh has full reach; keeping this node
off the overlay sidesteps it rather than fixing it.

## Outcome

## Deferred

## Gotchas
