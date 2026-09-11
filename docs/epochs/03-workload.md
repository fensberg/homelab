# Epoch 03 — Workload

- **Tier / path:** `environments/`
- **Branch:** `epoch/03-workload`
- **PR:** —
- **Status:** In progress

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

  > **Both halves of that sentence are wrong, and the correction is in
  > [Superseded: isolation is proportional to exposure and blast radius](#superseded-isolation-is-proportional-to-exposure-and-blast-radius).**
  > The ports are 2456-2457, and the crossplay backend uses a relay so no
  > inbound path is needed at all. It is left here because this claim is what
  > set the constraints for everything below it, and a requirement that drove a
  > design is worth reading beside the correction rather than being quietly
  > replaced.

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

> **Superseded in its premise.** This section reasons from the game server
> needing an inbound port forward. It does not - see the correction under
> Decisions. The mechanism it argues for is still what the zone is; what
> changed is which workload earns one, and it is no longer this one.

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

> **Superseded in its premise.** This section reasons from the game server
> needing an inbound port forward. It does not - see the correction under
> Decisions. The mechanism it argues for is still what the zone is; what
> changed is which workload earns one, and it is no longer this one.

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

> **Superseded in its premise.** This section reasons from the game server
> needing an inbound port forward. It does not - see the correction under
> Decisions. The mechanism it argues for is still what the zone is; what
> changed is which workload earns one, and it is no longer this one.

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

#### Settled: the workload's state goes to a bucket the teardown keeps

The framing above - that the world save is pinned to a node - is right about
the consequence and wrong about the mechanism, and the correction is on #330.

An image change does not replace the node VMs. They clone from a template by
`vm_id` and carry no `lifecycle` block, and #97 already records that an image
change cannot reach a running estate at all. What loses the data is that the
delivery mechanism is a **rebuild**: a demolish followed by an ignition, and
demolish destroys every disk by design. That is the contract - "TNT is TNT" -
so no disk-lifecycle trick can help, and teaching the teardown to skip a volume
would make a destructive operation partial, which this estate refuses.

Three options, and only one survives.

**Network storage from the hypervisor** - NFS or iSCSI to a dataset - survives
a rebuild and hands the untrusted machine a direct path to the one thing on the
estate that is not disposable. Rejected on the zone's own terms.

**A preserve-on-teardown flag** makes `demolish` partial and leaves a volume
nothing tracks. Rejected.

**Object storage, in a bucket of its own.** Taken. It needs only outbound
egress from the zone, which the workload has anyway and which opens no path
into the estate - the same direction its own traffic already goes.

The bucket is `<state bucket>-workloads`, derived rather than configured so it
needs no vault item and cannot drift from the bucket beside it. Sterilize
**forgets** it before the destroy, and the next ignition adopts it back.

That forgetting is a deliberate exception to the rule in `teardown.go` - that
losing track of something which outlives the VMs leaves a real thing nothing
tracks. It does, for exactly as long as there is no estate to track it.
Adoption closes the window, and the alternatives are a teardown that stops
part-way on a bucket Cloudflare will not delete, or one that succeeds by
deleting the backups.

The order of those two steps is the safety, and
`TestTheWorkloadBucketIsReleasedBeforeAnythingCanDeleteIt` holds it: released
first, a failure to release stops short of deleting anything; reversed, the
deletion has already happened by the time anyone finds out. The mutation
proving it reorders rather than deletes, because reordering is what somebody
writes while looking at a stuck teardown instead of at this file.

**What this does not do** is make the node's local disk durable. It is not, and
it should not be - the machine is chosen to be destroyable. Anything that must
outlive a rebuild goes to the bucket; the disk is a working copy.

#### Inherited from epoch 02: tainting the control planes

Moved here on 2026-09-07, because it is the same question wearing a different
hat and epoch 02 was blocking on an answer this epoch owns.

The step is `allowSchedulingOnControlPlanes = false` in `management/cluster/talos.tf`,
with tolerations for whatever stays. Everything that can move is already off the
control planes by a **required** node affinity, so the taint is not what gets CI
or the operators onto workers - that is done. What the taint adds is coverage for
anything added later that forgets an affinity, which is worth having and is not
worth blocking an epoch on.

It is blocked on exactly one thing, and it is the paragraph above. `tofu-state-1`,
`-2` and `-3` are Local PV Hostpath volumes pinned to control-plane nodes. Taint
those nodes and CloudNativePG tries to reschedule the state database onto a
worker, and the directory holding the data stays where it is. That is the Valheim
trap arriving early, against the database that holds this estate's own OpenTofu
state - so it is the same decision, and making it once covers both.

Two things that are **not** blockers, both established by reading source rather
than assuming, so nobody re-derives them:

- **The OpenEBS helper pod follows the volume on its own.** `helperPod` exposes
  no tolerations and no `nodeSelector`, which reads like a helper that cannot
  reach a tainted node. provisioner-localpv v4.6.0 reads the taints off the Node
  it is provisioning for and builds a matching toleration for each. There is
  nothing to set.
- **`localpv.privileged: true` renders nothing here.** It is consumed only by
  the subchart's DaemonSet (needs `nodeDeployment.enabled`, false) and its
  PodSecurityPolicy (needs `rbac.pspEnabled`, false, and PSP has not existed
  since Kubernetes 1.25).

What is genuinely open is where stateful data lives when the machine under it
becomes ineligible - a replicated engine, a CSI driver that can move a volume,
or an accepted pinning with the taint carrying a toleration for the database.
See also #315, which is the security half of the same component.

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

### Superseded: isolation is proportional to exposure and blast radius

The decision this replaces read the problem as **untrusted code**, and built
from there: a game server is code nobody here wrote, therefore it is suspect,
therefore it gets a machine of its own. That reasoning produced a design which
is still correct in its mechanism and wrong in its default, and it is worth
saying exactly where it went wrong because the same mistake is easy to repeat.

**Provenance is almost never the threat here.** The operator's own framing, and
it is the right one: "The code itself is hardly ever the threat because we're
vetting it. The traffic and the blast radius is the threat." Nothing
self-hosted here is novel software written by a stranger with intent. It is
Home Assistant, a game server from Steam, a fork of somebody's published
project. The code is vetted. What is not vetted is who is allowed to send it
packets, and what those packets reach if they win.

So the axis is two questions, and neither is about who wrote it:

- **Exposure** - what can send this traffic?
- **Blast radius** - if that traffic wins, what does it reach, and what does it
  hold?

Vetted code compromised through hostile traffic is exactly as compromised as
malicious code would have been. The isolation still earns its place. "We do not
trust this binary" was simply never the reason.

#### Which produces a tiering, not a zone

| Tier                           | For                                       | Gets                                            |
| ------------------------------ | ----------------------------------------- | ----------------------------------------------- |
| **Shared workers** _(default)_ | Most things. LAN-reachable, small radius. | A namespace each and NetworkPolicy between them |
| **Dedicated zone**             | High exposure **or** high blast radius    | Its own subnet, vnet, machine and taint         |
| **Case by case**               | Workloads whose reach is the point        | Argued in its own record                        |

**Shared workers is the default and the zone is the exception.** The previous
decision implied the reverse, and the reverse does not survive contact with
what this estate is for: a model where every third-party application gets a
virtual machine stops at about five workloads on the memory this hypervisor
has, and the plan is ten or twenty. Most of them are LAN-only with a small
radius, and a namespace with an enforced policy is the proportionate answer.

That tier is now possible for the first time. NetworkPolicy between namespaces
on shared workers was not enforcement under Flannel - it was decoration - which
is the argument that put Cilium in.

#### The examples, classified

| Workload                 | Exposure                             | Blast radius                                                                                      |
| ------------------------ | ------------------------------------ | ------------------------------------------------------------------------------------------------- |
| A game server, crossplay | Outbound relay only, nothing inbound | A world save                                                                                      |
| Home automation          | LAN, possibly WAN through a tunnel   | **Physical** - locks, heating, cameras - and it must reach every device on the LAN to work at all |
| A mail relay fork        | Inbound SMTP from the whole internet | Mail, and it is the one carrying edited code                                                      |

**Home automation is the case that breaks the zone**, and it is worth keeping
in the record because it is the highest-stakes thing on the list and the zone
would fail it. Its blast radius is physical, and the containment the zone
provides - a subnet that reaches nothing - is precisely what stops it working,
because reaching every device on the LAN _is_ the application. It needs an
argument about what it may reach outward and who may reach it, not a subnet
that isolates it from its own purpose.

**The mail relay is the zone's real first tenant.** Inbound SMTP from the
internet, mail at stake, and custom code on top. Every part of the original
three-layer argument applies to it, and applies more strongly than it ever did
to a game.

#### What the zone still is, and why it was worth building

Unchanged in mechanism, and the record below it stands: a dedicated machine on
its own subnet and vnet, from an image without the overlay extension, tainted
so nothing else lands there. The three layers still answer three different
questions - what it may talk to, what shares its kernel, what the machine
itself can reach - and the audit of what an escape obtains still holds.

What changed is when to reach for it. It is not where workloads go. It is what
a workload gets when its exposure or its blast radius earns it, and the
per-workload zone model means the next one that does costs a config entry.

#### The game server moves to the shared workers

By the axis above it is the least demanding thing on the list: no inbound path
at all under crossplay, and a world save as its entire blast radius. A
dedicated machine for it was proportionate to a threat model that turned out
not to describe it.

One thing follows it there and is not optional. The shared workers are where
the CI runner lives, and that runner holds a vault token with read and write
over the whole vault. **Blast radius is not only what a workload holds - it is
what is reachable from where it sits.** The game server's own radius is
trivial; its neighbour's is the estate.

That is not an argument for a zone, and two answers were considered before
landing on neither.

**A node anti-affinity** between the workload and the runner. Rejected on
shape: the rule is written on every workload, against the runner, so it is N
rules and forgetting one silently puts something on the same kernel as a token
with read and write over the whole vault. Fail-open, and it gets worse as
workloads are added.

**A worker dedicated to CI**, tainted, with the runner tolerating it. Much
better shape - one rule, written once, on the thing that actually holds the
credential, and a forgotten toleration means a workload does not land there
rather than that it does. It is the taint-over-convention argument this epoch
already made, applied to the privileged side: **isolate the crown jewels, not
each visitor from them**, because the privileged things can be enumerated and
future workloads cannot.

**Separated, but by adding workload nodes rather than by reserving a CI one.**

The density objection to a dedicated CI worker was real: the estate is
deliberately being packed, CI is bursty, and holding a machine idle for it
fights what this epoch is for. That objection dissolves once the split is
described from the other side. CI keeps the workers that already exist and are
already sized for it; the workloads get nodes of their own, which is capacity
being added for work that is about to run rather than capacity standing by.

Same separation, and the memory is spent on the things being packed instead of
on the thing that idles.

So the classes are:

| Node class        | Runs                                             | Taint                         |
| ----------------- | ------------------------------------------------ | ----------------------------- |
| **Control plane** | etcd and the API                                 | Untainted today; see epoch 02 |
| **Privileged**    | CI, and anything else holding estate credentials | Tolerated only by that work   |
| **Workload**      | Things found on the internet and self-hosted     | Tolerated only by workloads   |

#### And it needs a guard, because a declaration nobody checks is a convention

The operator's requirement, and it is the right one: privileged work must not
end up on a machine running things somebody found on the internet.

Placement by convention fails the way every convention here has. A workload
added without the right toleration does not fail - it schedules somewhere, and
the somewhere is decided by whatever the scheduler finds convenient. Nothing
reports it, and the first sign is an incident.

The guard has two halves, and both are needed because each alone is
satisfiable while the property is false:

- **Every workload manifest places itself on workload nodes**, by toleration and
  node selection. A workload with neither is one the scheduler may put beside
  the vault token.
- **Nothing privileged tolerates the workload taint.** The reverse direction,
  and the one that would otherwise be missed: it is the privileged side moving
  that puts the two together, and CI already carries tolerations for reasons of
  its own.

It lands with the node classes rather than before them. There is nothing to
assert while `environments/` is empty and both classes are one undifferentiated
pool - a guard written now would pass by finding nothing, which this repository
has a test specifically to refuse.

#### Is there actually a vector? Walked, rather than assumed

Four answers were written for this before anybody asked how the attack would
work. That question turns out to settle it, and it should have come first.

**Pod to pod: no.** The runner's token is a Kubernetes Secret mounted into the
runner's own mount namespace. A neighbouring pod cannot read it off disk, and
cannot read it through the API either - RBAC does not grant it, and a workload
with `automountServiceAccountToken: false` has no identity to ask with.

**Escape to the node: yes, and it is the only one.** The kubelet stores mounted
secrets under `/var/lib/kubelet/pods/<uid>/volumes/kubernetes.io~secret/`, which
root on the host can read. The kubelet's own credential reaches the same place
by Node authorization. So the chain is: remote code execution in the workload,
then a container escape to node root, then the token.

**The second step is the hard one.** Talos has no shell, no package manager and
a read-only root, and enforces Pod Security - a workload running non-root
without `privileged` or `hostPath` needs a kernel vulnerability to escape, not a
misconfiguration.

Two things lower it further, and both are recent:

- Nothing on these workers is internet-**inbound** now that crossplay removed
  the port forward. The chain has no obvious place to start.
- The self-hosted runner does not execute pull-request code. `pr-validation`
  runs on GitHub-hosted runners, and the integration lane is deliberately not
  reachable from a pull request.

**So: real but narrow.** Two hard steps, one of them a kernel exploit.

#### Which makes the response proportionate rather than urgent

The consequence is total - read and write over the whole vault is the estate -
and self-hosted software does get remote code execution. That asymmetry is what
makes it worth something rather than nothing.

- **It does not block a workload.** A relay-only game server running non-root
  does not start the chain.
- **The separation is taken when workload nodes are added anyway**, where it
  costs a taint and a toleration. It was never worth a machine standing idle,
  which is what the first three answers each talked themselves into.
- **Narrowing the token is the higher-value fix**, and it is the one this
  estate's own rule points at: narrow what a credential may **do** before
  hardening where it is **kept**. Kernel isolation lowers the probability;
  narrowing lowers the consequence, and a lowered consequence keeps holding
  after the isolation has failed and after everyone has stopped watching.

The guard above still lands with the node classes. What changed is that it is
enforcing a proportionate decision rather than an escalating one.

#### Which splits isolation into two dials rather than one

Worth stating separately, because the zone conflates them and the CI question
is what pulls them apart:

| Dial                  | Answers                          | Bought with                          |
| --------------------- | -------------------------------- | ------------------------------------ |
| **Kernel isolation**  | What shares its kernel?          | A dedicated node and a taint         |
| **Network isolation** | What can it reach, and reach it? | Its own subnet and vnet, plus policy |

They are independent, and most things want neither:

- **CI** would want the first and not the second. Its blast radius is the whole
  estate, so sharing a kernel with nothing has real value - but it must reach
  the hypervisor, the API server and the vault, so a subnet isolating it would
  stop it working.
- **A mail relay taking inbound SMTP** wants both.
- **A game server on a relay** wants neither, which is how it ends up on the
  shared workers.

The zone as built is both dials at once. That is right for what earns it, and
it is why CI would get a node rather than a zone if it gets anything.

#### How the original decision went wrong, since it is repeatable

Three of the four claims that produced it came from a summary rather than from
the vendor, and the fourth followed from them:

- The port range was given as UDP 2456-2458. Iron Gate's guide says the server
  uses the given port and port+1, so 2456-2457. The third port is widely
  repeated and is not in the documentation.
- The inbound port forward was treated as unavoidable. The crossplay backend
  uses a relay: the server connects outbound and no forward is needed.
- From those, "the first genuinely inbound path into this estate", which was
  the sentence carrying the whole design.

The requirement doing the most architectural work is the one to verify first,
and it cost one page of vendor documentation to check - read after the design
was built rather than before it.

### Cilium arrives from OpenTofu, between bootstrap and the health gate

**Chose:** a manifest rendered from the pinned chart, committed to this
repository, and applied by OpenTofu after `talos_machine_bootstrap` and before
`data.talos_cluster_health` - using the `terraform_data` + `local-exec` +
`kubectl` pattern `gitops.tf` already uses to bootstrap Flux.
**Rejected:** Talos `inlineManifests`; the Cilium CLI; adding the Helm provider.
**Because:** three of the four cannot be upgraded, cannot be reviewed, or cannot
be seen by the guard that checks suppliers.

#### Why the obvious answer is the wrong one

`inlineManifests` looks correct and is the first thing anyone reaches for: the
manifest travels with the machine config, so it is applied as the cluster comes
up and the ordering problem disappears. Sidero's own documentation closes it:

> Talos only creates missing resources from inline manifests - it never deletes
> or updates them.

So Cilium would be installed once and reconciled by nothing. Changing it means
editing the control-plane machine configuration and running
`talosctl upgrade-k8s` by hand, which is an imperative path outside the button
and precisely the shape this estate refuses everywhere else. The second cost is
review: a rendered CNI chart is thousands of lines, and this would put them
inside the machine configuration - the most privileged document the estate
produces, and the one whose diffs most need to be readable.

The **Cilium CLI** is a new supplier, a new binary, and imperative. It is
documented by Sidero for development and testing, which is what it is for.

The **Helm provider** is the closest call, and is rejected for this path only.
A provider is not a library: it is a binary downloaded at init and executed
locally with a live credential, which is why `approved-suppliers.yml` treats
providers as the most privileged deliveries here. It would also fetch the chart
at apply time, so the image digests would never appear in this repository - and
a digest that is not committed is one `tests/go/repo/suppliers_test.go` cannot
read. Adopting Helm later for the workload tier is a separate question that
this decision does not foreclose.

#### What the chosen route buys, and what it costs

It buys four things: the digests are committed, so the supplier guard can
actually see them; the diff of a Cilium bump is a diff of Kubernetes objects
rather than of a machine configuration; the apply mechanism is one already
reviewed and in the tree; and no new supplier is required.

It costs a large generated file in git, and a regeneration step. **That step
must be codified rather than remembered** - a `task` verb that re-renders from
the pinned chart version, with a test asserting the committed manifest matches
what that version renders. A generated artefact nobody can regenerate
deterministically is worse than no artefact, because it silently becomes the
source of truth.

#### The ordering works because the control plane does not need a CNI

Worth writing down, because it is the fact the whole sequence rests on and it
is not obvious. `kube-apiserver`, `etcd`, `kube-controller-manager` and
`kube-scheduler` run as static pods on host networking. They come up with no
CNI at all. So the API answers while every node is still `NotReady`, and
OpenTofu can apply a manifest into a cluster that has no pod network yet.

The sequence is therefore: bootstrap, apply Cilium, nodes reach `Ready`, health
gate passes. `data.talos_cluster_health` gains a dependency on the apply.

The failure mode if that edge is missing is not subtle and is worth naming so
it is recognised: with `cni.name: none` and nothing installing a CNI, no node
ever reaches `Ready`, and the health gate waits its full ten-minute timeout
before failing. Ten minutes of apparent hang is what a missing dependency looks
like here.

#### KubePrism, and a single point of failure this declines to inherit

`proxy.disabled: true` hands service routing to Cilium, and Cilium's agents
then need to reach the API server themselves. They cannot do it through a
Service ClusterIP, because nothing implements ClusterIP until Cilium is the
thing implementing it.

The obvious address is the cluster endpoint, and this estate hardcodes that to
`local.node_ips[0]` - one named control plane, which is issue #316. Pointing
Cilium at it would promote a known API single point of failure into a **pod
network** single point of failure: lose that one machine and no node on the
cluster has working networking.

Talos's answer is **KubePrism**, a TCP load balancer Talos runs on every
machine at `localhost:7445`, spread across all control-plane endpoints and
health-filtered. Cilium is configured `k8sServiceHost=localhost` and
`k8sServicePort=7445`, per Sidero's documented values.

This does **not** fix #316. The kubeconfig and the cluster endpoint still name
one machine, and that issue stays open. It declines to make it worse, which is
a different and smaller claim.

**KubePrism is declared explicitly even though it is enabled by default.**
Relying on an upstream default for load-bearing behaviour is a blind spot: the
day it changes, the pod network fails and nothing in this repository ever said
it was required. The estate's own rule is that a guard which is off by default
is indistinguishable from nothing being wrong.

#### The values Talos requires, read rather than guessed

From Sidero's Cilium guide for the kube-proxy-free variant, recorded here so
nobody re-derives them from a blog post:

| Value                                      | Setting                                                                                          |
| ------------------------------------------ | ------------------------------------------------------------------------------------------------ |
| `ipam.mode`                                | `kubernetes`                                                                                     |
| `kubeProxyReplacement`                     | `true`                                                                                           |
| `k8sServiceHost`                           | `localhost`                                                                                      |
| `k8sServicePort`                           | `7445`                                                                                           |
| `cgroup.autoMount.enabled`                 | `false`                                                                                          |
| `cgroup.hostRoot`                          | `/sys/fs/cgroup`                                                                                 |
| `securityContext.capabilities.ciliumAgent` | `CHOWN,KILL,NET_ADMIN,NET_RAW,IPC_LOCK,SYS_ADMIN,SYS_RESOURCE,DAC_OVERRIDE,FOWNER,SETGID,SETUID` |

The cgroup pair is the Talos-specific half: Talos mounts the cgroup hierarchy
itself, so Cilium must be told not to.

#### This is a rebuild, and the rebuild has one-way doors

A CNI cannot be swapped on a running cluster - nodes must not be `Ready` when
it arrives - so this is `demolish` followed by a fresh ignition. The operator
has confirmed the cluster VMs are disposable and hold nothing, which is what
makes that acceptable rather than merely necessary.

Two consequences that must be said before the command is run rather than
discovered during it:

- **The teardown empties the object storage bucket.** `emptyObjectStorage` in
  `scripts/contractor/internal/phases/teardown.go` deletes every object in the
  site's bucket, because Cloudflare refuses to delete a bucket that is not
  empty. The age-encrypted state backups live in that bucket. So the operation
  most likely to precede needing a state backup is the one that destroys every
  state backup. This is fine here only because a fresh ignition starts from
  empty state by design and there is nothing worth restoring - it is not fine
  in general, and it is not a property to rely on twice.
- **The OpenTofu state lives inside the cluster being destroyed**, in Postgres.
  `demolish` consumes that state to know what to destroy, which is the correct
  order. A teardown that stops partway has already done irreversible work and
  leaves machines nothing tracks, so preconditions belong before the first
  irreversible step.

#### What this does not deliver

Cilium makes NetworkPolicy _enforced_. It does not write any policy, and an
estate with an enforcing CNI and no policies is exactly as open as one with a
non-enforcing CNI and many. The policies are their own piece of work.

It is also only one of the three isolation layers this epoch's untrusted-zone
decision names. A dedicated node answers what shares its kernel, and omitting
the overlay answers what the machine itself reaches; neither is affected by the
CNI. Cilium is necessary and is not sufficient, and the temptation once it
lands will be to treat the isolation question as closed.

### The control plane oversees the zone; the zone sees nothing

Settled in discussion before the policy work, so it is inherited rather than
re-derived.

The intent is asymmetry: the control plane is aware of everything and
orchestrates it, and from the workload's side the machine it runs on should
look miraculous - administered by something it cannot see or reach.

**That asymmetry mostly already exists, and not because of the network.** Node
authorization and NodeRestriction mean the zone's kubelet may read Secrets and
ConfigMaps only for pods bound to itself. It cannot list other nodes or other
pods, and it cannot modify its own Node object beyond a narrow set of fields -
which is the same mechanism that refuses it its own taint, recorded above.

**One part of the intent has to be inverted: Kubernetes pulls.** The kubelet
opens the connection and watches the API server for pods assigned to it; the
control plane does not push work down. A node that cannot reach the API is not
a member, so "reaches nothing at all" is not available. What makes that
acceptable is the paragraph above - the reach exists and what it obtains is one
workload's own secrets.

#### The oversight channel is API server to kubelet, and it must be open

`kubectl logs`, `kubectl exec`, `port-forward` and metrics scraping all travel
API server -> kubelet on **10250**. That is the direction "the control plane
oversees the zone" actually runs on, and it is the safe one: the control plane
reaching down grants the zone nothing.

Closing it costs the ability to read a log from the workload this whole epoch
exists to host, which is a thing nobody misses until the evening it matters.

**Only the API server talks to that node.** Not etcd, not the scheduler, not
the controller manager. "The control plane" is four components and one of them
has business here.

#### Why the workload can be denied everything while the node is not

NetworkPolicy governs **pods**. The kubelet is a host process on the host
network, so pod policy does not apply to it.

So the workload can be denied all cluster access - no API server, no node
subnet, no hypervisor - while the machine underneath it stays a fully
orchestrated cluster member. The workload sees nothing, the node is
administered normally, and the control plane sees everything. That is the
intent above, and it falls out of the layering rather than needing to be built.

The shape the policy work inherits:

| Direction             | Rule                                                                                                                          |
| --------------------- | ----------------------------------------------------------------------------------------------------------------------------- |
| Workload egress       | Deny by default. DNS and the internet only - the game, and its backups. Not the API, not the node subnet, not the hypervisor. |
| Workload ingress      | Its own ports, from the port forward. Nothing else.                                                                           |
| API server -> kubelet | Allowed, on 10250. This is the oversight channel.                                                                             |
| Service account token | `automountServiceAccountToken: false`. It has no use for the API, so it is not handed a token.                                |

That last row is worth doing even though the egress rule already makes the
token useless: a credential that cannot be spent is still a credential that was
handed over, and the cheaper habit is not to mount it.

### Removing the untrusted zone

Written while the zone is being built rather than when it is being removed,
because the thing that makes deprecation painful is never the design - it is
that nobody wrote down which parts were optional.

The workload this zone was built for will be deprecated one day. Removing it
must be a config change and a converge, never a rebuild, and this is the path.

#### The order matters, innermost first

1. **The workload.** Delete its directory under `environments/`. Flux syncs
   `./clusters/management` with `prune: true`, so the objects go with it. This
   is the only step that needs no privilege beyond a merge.
2. **The machine.** Remove that workload's entry from the site's `dmz_zones`,
   then `contractor converge`. A machine in a zone is not an etcd member, so
   this is a drain and one destroy rather than a quorum event. Removing one
   zone renumbers no other: the zones are sorted by name and each keeps the
   subnet it was given, so a neighbour being deprecated never moves a surviving
   workload's addresses out from under its firewall rules.
3. **The port forward.** On the router, by hand. Nothing in this repository can
   see it, which is the reason it is listed here at all.
4. **The network**, below.

Doing this in the other order takes a workload's network away while the
workload is still running, which is a diagnosis nobody enjoys.

#### What removing the last zone does on its own

Everything derived from the machines stops existing: no VM, and no second
image, because `proxmox_download_file.dmz_disk_image` keys off
`local.dmz_hypervisors` rather than off the site. The SDN tasks **loop over the
zones** rather than being gated on a count, which is the stronger form of the
same property - a `when:` has to be remembered on every task, and a loop over
an empty list cannot run at all.

`tests/go/repo/untrusted_zone_offswitch_test.go` holds both halves of that in
place - that every zone resource keys off its machines, and that every zone
task carries the gate - and
`TestNoUntrustedWorkloadDerivesNoZone` covers the config half. They exist
because both mistakes read as correct in review: keying off the site is what
the resource above does, and a task that keeps its idempotency check still
looks guarded.

#### What is left behind, and how to clear it

**Ansible creates and never removes.** So three things survive
`dmz_count: 0`, all inert, none of them dangerous, and none of them obvious to
whoever finds them later:

- each zone's vnet (`vnetdmz0`, `vnetdmz1`, ...)
- each of their subnets
- the second Talos image in `local-iso`, once no zone is left

Clearing them is a hypervisor operation, listed before deleted because the
subnet id is generated and should be read rather than guessed:

```sh
# What is actually there
pvesh get /cluster/sdn/vnets --output-format json | grep vnetdmz
pvesh get /cluster/sdn/vnets/<vnet from the listing>/subnets --output-format json
pvesm list local-iso | grep '/dmz-'

# Remove, innermost first, then apply the SDN change
pvesh delete /cluster/sdn/vnets/<vnet>/subnets/<id from the listing>
pvesh delete /cluster/sdn/vnets/<vnet>
pvesh set /cluster/sdn

pvesm free local-iso:iso/<image from the listing>
```

`pvesh set /cluster/sdn` performs a network reload on the hypervisor - see the
note in `hypervisor-prep.yml` about what that call actually does. It is the
same operation the playbook runs conditionally, and it is not free enough to
run for no reason.

#### What is not removed, deliberately

**Cilium stays, and that is not an oversight.** Swapping the CNI back would
need another rebuild in the other direction, and nothing wants Flannel back:
enforced NetworkPolicy is what lets the hypervisor grant recorded in
[`02-abstraction.md`](02-abstraction.md) be narrowed to the runner pod, which
that record names Flannel as the reason it could not be. The untrusted workload
motivated the upgrade; it does not own it.

**The zones' addressing stays too.** Each subnet, gateway and VNI is derived
from the site's octet and the zone's position in the sorted list rather than
chosen, so they cost nothing while unused. A zone that returns under the same
name returns to the same addresses. Deleting that would be deleting arithmetic.

### The move to shared workers was not costed on memory

Found while writing the Deployment's resource requests, which is late.

The workers are 8 GiB each. The vendor's guidance for this workload is 4 GiB on
a fresh world and 6-8 once it is explored and built on, and the request is 4 GiB
with a 6 GiB limit. So a mature world takes most of a worker, on machines that
also host the CI runner and every operator with an anti-control-plane affinity.

The isolation argument for moving it there was right and is unaffected: the
workload's exposure is a relay and its blast radius is a world save, so it does
not earn a zone. What was not weighed is that the zone came with a machine
sized for it, and the shared workers were sized before this workload existed.

**Settled: three workers at 10 GiB, and the workload asks for 8.**

The operator's call, and the arithmetic is worth keeping because the obvious
number is wrong. A node's Allocatable is its memory minus what Talos, the
kubelet and the Cilium agent reserve - roughly a gigabyte. **A pod requesting
8Gi therefore does not fit on an 8 GiB node at all.** It stays Pending
indefinitely with an "Insufficient memory" event, which reads as a scheduling
puzzle rather than as a machine that is simply too small.

Ten leaves about nine allocatable: the 8Gi request fits, with room for the
daemons that must run everywhere. Three of them spends fourteen of the 23 GiB
measured free on the hypervisor.

Request and limit are both 8Gi, which makes the pod Guaranteed rather than
Burstable - not evicted ahead of others under node pressure, and unable to grow
into a neighbour's memory. It also **effectively dedicates a worker**: nine
allocatable minus eight leaves very little else. That is a real consequence of
asking for the vendor's upper bound, and it is worth noticing that the estate
has arrived at a dedicated machine for this workload by resource sizing, having
decided against one on isolation grounds. Both decisions are right for their own
reasons; the outcome looking similar is a coincidence rather than a plan.

**There is no horizontal answer.** A dedicated server is one process simulating
one world - Kubernetes cannot split a process across nodes, and a second replica
would either fail to schedule against the ReadWriteOnce volume or corrupt the
world if it did. `replicas: 1` and the Recreate strategy exist for that reason.
The node has to fit it, which is why sizing is the only dial.

### A tag releases the estate, not a resource

Asked while reading production's overlay, which lists its modules as a flat
`resources:` list. If a second module joins that list and a tag ships it, does
the first module get re-released too - and what happens when one module is
mid-rewrite and another is ready?

**The premise is right.** `flux-production` resolves `semver: ">=0.0.0"` against
the repository's tags, so the tag names a commit of the whole tree. There is no
per-resource tag and nothing in the overlay narrows it. Tagging to ship one
module does re-apply every other module as it stands at that commit.

**What makes that survivable is that three different questions are answered in
three different places**, and only one of them is the tag:

| Question                  | Answered by                        |
| ------------------------- | ---------------------------------- |
| Which modules are live?   | the overlay's `resources:` list    |
| What code does one run?   | the image digest in its Deployment |
| When does any of it move? | the `v*` tag                       |

So a release is a snapshot of what the estate declared, and the version of a
given workload is its digest. Upgrading the game is a digest change, which is a
commit, which is a tag - the tag is the act of publishing, not the version
number of the thing published.

**The re-release is usually a no-op.** If a module's rendered manifests are
byte-identical between two tags, the apply changes nothing; the server does not
restart and the world is not touched. Coupling only bites when the other
module's declaration actually changed in that range.

#### The half-finished module is a branch problem, not a tag problem

Which is what makes the awkward case rarer than it looks. In-progress work lives
on a branch off the epoch branch and merges when it is done, so the tree at the
tag point holds only finished work by construction. A half-rewritten module
reaching a tag means it was merged before it was ready, and no release
granularity fixes that.

The genuinely residual case is narrower: two _finished_ changes are both on the
branch, and only one should go out now. A single repository tag cannot separate
them. The answer is to treat the merge as the readiness signal rather than the tag,
which the epoch model already does since pieces merge when they are done. Where
something slips through anyway, revert it and tag forward.

**Roll back by tagging forward, never by deleting a tag.** `>=0.0.0` takes the
highest tag it finds, so deleting one does roll production back, silently, by
re-resolving to the next highest. That is a destructive git operation with an
invisible deployment as its side effect. Reverting the commit and tagging a new
version says the same thing in a way that leaves a record.

#### Why per-module tags are not the cheap alternative they sound like

Flux's `semver` ref ranges over tags that parse as semver, and `valheim/v1.2.0`
does not parse. The fields that would select a per-module tag - `ref.tag` and
`ref.name` - pin one exact ref, so releasing would mean editing a pinned string
inside `environments/production/`. That is precisely what the two-source design
exists to prevent: nothing promotes by somebody editing a file in production's
directory.

Per-module cadence is available, and the ecosystem's answer to it is
image-reflector-controller and image-automation-controller, which watch a
registry per image and commit the digest bump themselves. That moves the release
decision to the image rather than to git, which is a coherent model and a larger
one - another controller, another set of credentials, and a bot with write
access to the branch. Not now, and not for two workloads. The trigger to
revisit is a module that genuinely needs to move on a different rhythm from the
rest of the estate, rather than the count of modules going up.

## Outcome

## Deferred

## Gotchas

### The expediter, and what building it turned up

Valheim 1.0 shipped on 2026-09-09 and patched twice in the next two days.
Clients update themselves through Steam and refuse a server on an older build,
so each patch stranded every player until the server image was rebuilt - and a
rebuild meant a person dispatching a build, copying a digest out of a log,
editing a manifest, opening, merging and tagging. Six steps, none of them a
decision. The log also prints the local image id right beside the manifest
digest, so the transcription had a wrong answer sitting next to the right one.

**The expediter** does all six. It asks Steam for the public build of app
896660, compares it with the build recorded beside the pinned digest, and when
they differ builds the image for exactly that build, opens a pull request
changing those two lines, merges it once every required check passes, and tags
the next release. The name is the construction role that chases orders with
suppliers and gets material to site; Valve was already an approved supplier, and
the estate already spoke of taking delivery.

#### A standing order, bounded by a check it cannot skip

`main` requires a pull request with a code owner's approval and has no bypass
actors, and a pull request opened with `GITHUB_TOKEN` never triggers the checks
that would let it merge. So the expediter needs its own GitHub App, and for a
patch to reach players without waiting on a person, that App has to be allowed
past the review.

It is allowed past the review and **nothing else**. It is a bypass actor on the
ruleset that requires review, and not on the ruleset that requires checks. One
of those checks is `security guard-standing-order`, which refuses any pull
request by the expediter that changes anything but the digest and the recorded
build, or that moves the digest to a different image repository - the change
that reads exactly like a routine update in a diff. Construction has the word:
a standing order is approval given once for one specified recurring item, not
for whatever the holder feels like buying.

Two properties hold it together. Without the bypass, the merge is refused and
the pull request waits for a review, so the same machinery degrades to a person
at the end. Without the `EXPEDITER_LOGIN` variable, the workflow does not attempt
the merge at all, so the bypass is never used while the check bounding it has no
holder to judge.

Why not an existing role: the foreman's key lives inside the cluster, so giving
it the bypass would let a cluster compromise merge to `main` without review; the
clerk's whole design is that it has no lever; and the inspector signs off on
work rather than bringing material.

#### The image says which build it holds, and refuses to lie about it

The Dockerfile takes the Steam build as a build argument and refuses to build
if Steam serves a different one - which happens if Valve publishes between the
check and the download. Without that, the label and the record in git would
describe files that are not in the image, and the next comparison would be
against a fiction.

#### Security is security again

The program that guards deliveries, pushes and merges and patrols the estate
was `security` first, became `gatehouse` while its only job was the gate, and is
`security` again now that the name had stopped fitting: a gatehouse is a
building, and a building does not patrol. The epoch 01 record keeps the old
name, because that rename is history.

#### The patrol was right that something was wrong, and wrong about what

It had failed every scheduled run for two days, saying "5 run(s) queued longer
than 30m, oldest 53h56m - work is not being picked up". The runner was healthy.
All five were `waiting`: deploy runs gated on environments nobody approved, and
a nightly gated the same way (#204). The patrol exists for work nothing will
start, not for approvals GitHub already notifies someone about, so `waiting` no
longer counts. **The right alarm for the wrong reason is worse than none,
because it teaches the reader to stop reading.**

Fixing that exposed a second defect it would otherwise have created. The
nightly check asked about every scheduled run in the repository, and the patrol
is itself a scheduled workflow. It was correct only because the patrol was
failing too; once green, its own successes would have satisfied the check that
exists to notice the nightly stopped. It asks about the drift check's workflow
alone now. And an unreachable API had produced "the estate is answering for
itself" over a patrol that had asked nothing, because a check that could not
look returned "skip" and skips did not count. Not knowing counts against health
now.

The cadence claim went with it (#221). The schedule is hourly on paper and four
to five hours apart in practice, which GitHub documents and nothing here can
change; the workflow now says five, because a switch believed to have one-hour
windows gets thresholds tuned to a check that is not happening.

#### Where the waiting runs came from

Every `v*` tag started a deploy that waited for production approval and would
then have failed, because the classifier declared every tag "a workload-tier
production release" without asking whether the directory it applies exists
(#370). GitHub does not evaluate path filters on tag pushes, so nothing else
stopped it. The classifier now asks both questions - did infrastructure paths
change, and does the directory the job would enter exist - and was run against
five scenarios before it shipped.

The same workflow's converge-failure path pushed a revert branch and never
opened the pull request for it, which left a branch nothing explained. That is issue 380, filed rather than fixed here because the fix needs its own token decision.

#### The refuse collector worked; nothing ran it

Every branch in the operator's picker belonged to a merged pull request, and
the collector recognised each one. It had been left for a person to run "when
the branch picker gets annoying", and the picker got annoying and nobody did.
It runs after every pull now. It was also about to stop seeing old pull requests
at a hard cap of five hundred, would have collected a branch reused for an open
follow-up, and claimed remote branches needed no collecting when GitHub only
deletes them on merge.

#### Two restatements, one instrument

Four workflows restated the Go version, and the pin file had claimed for its
whole life that a test prevented exactly that; the test exists now, and checks
shape rather than value, so a restatement that happens to agree today still
fails (#165). And Super-Linter's hang (#365) is instrumented rather than fixed:
debug logging so the next hang names the linter it stopped in, and a ten-minute
timeout against a two-minute run. The first attempt blamed the last line printed,
and being last is not being responsible.

### Sixteen allowlists, and the one file that owns them now

Chasing the hang above meant reading the egress policy, and the reading was the
finding: **no single place answered "may this job call out, and to what".** Each
job declared its own `allowed-endpoints` inline, sixteen lists across nine
workflow files, with nothing aggregating them. Adding an endpoint to one
workflow was invisible to any review not already reading that file.

The operator's rule, in their own words: "We always only have 1 source of truth.
We re-use modules where we can. Each of those workflows should point towards that
one file and that one file says which path supports which workflow. If a workflow
isn't stated it ISNT ALLOWED ANY. That's failed closed."

`scripts/approved-suppliers.yml` owns it now, under `egress:` - one entry per
job, named `<workflow file>/<job key>`, carrying the policy and, for a blocking
job, the exact hosts. The ten `audit` jobs carry their reason there too, instead
of in a comment above the step that nobody finds while deciding whether a new job
needs an allowlist.

**The workflows still carry a copy, and the reason is a platform constraint
rather than a preference.** harden-runner installs its policy in its pre-step,
which GitHub runs at the start of the job before any step of it - which is also
why the building code requires it to be the job's first step. So nothing read
from the repository can reach it at runtime: not a file, not a local action
(which needs a checkout that has not happened), not an earlier step's output.
The only alternative is a gate job publishing the lists as outputs, which every
lane would then wait behind - the same trade this repository already refused for
`scripts/versions.env`. So the copy stays, mechanical, and a guard makes it
trustworthy.

`security guard-egress` is that guard, and it is a guard rather than a test
because it refuses rather than observes: `task validate` and the pre-push hook
run it before anything is published, and `tests/go/repo` runs the same verb so
CI answers the question too. Egress is also security's own subject - what leaves
the site.

It fails five ways, and the third is the one that matters:

- a job that hardens the runner and is not named in the suppliers list
- a job with no harden-runner step at all
- a policy that is not the declared one
- a blocking job whose hosts differ from the declared set, in either direction
- an entry naming a job that no longer exists

**It walks every job in every workflow rather than the jobs it was told about.**
That is the general rule the operator stated alongside it - "whenever we build
something it looses it's protection ... every guard [must] walk the ENTIRE repo
and if we build something new and we forget to cover it then it FAILS" - and it
is why an undeclared job is refused rather than skipped. A guard written against
a list is correct on the day it is written and covers nothing built afterwards,
while still passing. #384 carries the audit of every other guard against the same
three questions: does it discover its subjects, does an undeclared one fail, and
does it prove it examined anything at all.

### The relay is not the only path, and "nothing on the LAN" was too strong

The crossplay reasoning in this record concluded that the game server needs no
inbound path because players reach it through PlayFab's relay. That held. What
it missed is that the relay is where a connection _starts_, not necessarily where
it stays: a few seconds after a player joins, PlayFab Party upgrades to a direct
path to the player's own address.

For a player in the same house as the hypervisor, that address is on the LAN,
and the egress exclusion of `192.168.0.0/16` refused it. Every join failed
identically, on the imported world and on a freshly generated one, with versions
matched:

```text
Server: New peer connected,sending global keys
PlayFab network error ... code '63': failed to establish or maintain a connection
Failed to send, suspend TX on playfab/...
```

It took a long time to find, and the time went into theories rather than
evidence: stale lobbies, MTU, version skew, the imported save, `ingress: []`.
Each was ruled out properly and none of them was it. What found it was Cilium's
own drop monitor on the node, filtered to the pod's address, during a join:

```text
xx drop (Policy denied) ... 10.244.3.22:59135 -> 192.168.10.117:59079 udp
```

Egress, to the operator's laptop. **The tool that enforces the policy is also the
tool that reports what it denied**, and it should have been the first thing
asked rather than the sixth.

#### The rule, and why it is this narrow

Egress UDP to `192.168.0.0/16`, destination ports 49152-65535 only - the dynamic
range client ports are drawn from. Every well-known LAN service stays closed:
DNS, SNMP, UPnP/SSDP, mDNS, and all of TCP. What a compromised server gains is
the ability to send datagrams to high ports on household devices; what it does
not gain is querying the router, opening ports on it through UPnP, or reaching
any login.

That is a real change to the isolation reasoning and is written as one. The
policy's comment used to say the server "cannot reach anything on the LAN"; it
now says the LAN is closed except for this rule.

#### A test that was designed wrong

A phone-hotspot join was proposed as decisive, on the claim that a remote peer's
direct path would use a public address the policy allows. It does not: Party
tries the client's local address as well, and hotspots hand out private
addresses too. The hotspot join failing was therefore read as ruling the LAN out
when it did not. Recorded because the error was in the design of the experiment,
not its result, and that kind is the easiest to repeat.

### One name, because two names for one thing become two things

The first world came up advertising itself as "combat berries delphine" while
the save file on disk was called something else entirely. Nothing was broken -
both values were exactly what the vault held - and it looked wrong to everybody
who saw it, which is its own kind of broken.

The cause was the shape rather than the values. `server_name` and `world_name`
were two vault items, so keeping them equal was a thing somebody had to
remember, and the default state of two fields nobody deliberately aligned is
misaligned. Valheim generates a name when one is not supplied, so the drift had
a source of its own.

They are now one item, `op://homelab/valheim/name`, used for both.

**The Secret still carries two keys.** The server takes the listing name and the
save-file name as separate arguments, and the image has no business knowing that
this estate happens to supply one value for both. Splitting them again later is a
config change that touches nothing here and nothing in the image. What collapsed
is the source, not the interface - which is the distinction worth keeping, because
collapsing the interface would have been the easier and worse change.

#### The hazard in this change is the world, and it is worth stating

The save file is looked up **by name**. A different value is not a rename: the
old world stays on the volume, untouched and unloaded, and an empty one is
generated beside it. So the vault item has to hold the existing world's name
exactly, and getting it wrong presents as "the server came up fine and everyone
has lost everything".

The comment on `Workload.Name` says so where somebody editing the field will
read it, rather than here where they will not.

#### This Secret is written by OpenTofu, so a tag is not enough

Worth writing down because it breaks the mental model the rest of the epoch
builds. Every other workload change is a merge and a tag, and Flux does the
rest. This one is not: `kubernetes_secret.valheim_server` is created by the
ignition tier, because a workload cannot be handed a password by a manifest in a
public repository.

So changing a name is `contractor converge`, not a reconcile. And a changed
Secret does not restart anything on its own - the pod reads its environment once
at start - so the rollout has to be asked for. Two steps that no other workload
change in this epoch needs, and both of them silent when skipped: the converge
looks successful and the pod keeps serving the old name.

That asymmetry is a cost of having no workload secret store, and it disappears
with OpenBao (#345).

### What the build guard bought, on its first run

The image that carries the PulseAudio libraries built green, and the `ldd` step
added with them passed silently in the middle of it - stage 5 of 8, no output,
no delay.

That is worth one paragraph precisely because there is nothing to see. The guard
exists so that the _next_ time a native dependency goes missing - a Valheim
update relinking against something new, a base image dropping a package, a
Debian rename - the build stops and names the library, rather than publishing an
image that starts, generates a world, logs in to PlayFab and serves a game
nobody can join.

The failure it replaces produced every signal of health. The pod was Running and
Ready. The health gate passed. Flux reported reconciled. Nothing anywhere was
red, and the only symptom was a blank field in a log line and a thirty-second
loop. **A check that fires at build time converts that into a red build with the
answer in it.**

### Two unrelated flakes, and a wrong diagnosis worth recording

#### SteamCMD fails its first app_update often enough to need retries

The image build failed on merge with

```text
ERROR! Failed to install app '896660' (Missing configuration)
```

and exit code 8, on a Dockerfile that had built successfully two hours earlier
and unchanged in that stage. The line above it is the tell: SteamCMD updates
itself and restarts on its first invocation, and an `app_update` issued in that
same run intermittently comes back with this.

Now retried five times, after a warm-up run that gets the self-update out of the
way. The stage also checks the server binary is actually present afterwards -
SteamCMD is known to exit 0 having downloaded nothing, and that stage is the
last moment anything can tell. After it, the files are copied into an image that
would build, publish, deploy and fail at runtime with the binary simply absent.

#### The Super-Linter hang was not Black and Ruff

Recorded because the reasoning was wrong in an instructive way, and the fix
shipped anyway on merits that still hold.

The Python linters were disabled, the conflict warning went away, and **the lane
hung in exactly the same place.** The last line is now "Successfully gathered
list of files..." - which is the line the Black/Ruff warning had been printed
immediately after all along.

So the hang was never at that warning. It has always been at the same point:
after the file list is built, as the linters begin. The warning was simply the
last thing printed before the stall, and being last is not the same as being
responsible. That distinction is the whole error, and it is a real hazard of the
otherwise-correct rule that a hang's final line is where to look: the final line
marks **where** it stopped, not always **why**.

The change stands on its own - six Python linters against zero Python files is
waste regardless - but it did not fix the hang, and #365 stays open. The next
thing to examine is what runs immediately after file-gathering, and whether
harden-runner's blocked egress on this job is stalling a linter that reaches the
network rather than failing it fast. That would also explain why it is
intermittent: which linters run at all depends on which files a given pull
request changed.

### The join code was empty because PlayFab Party wanted PulseAudio

The cluster came up clean and the game server ran: world generated, 183
locations placed, PlayFab login succeeded. And the join code was blank.

```text
New session server "..." that has join code ,  now 0 player(s)
Server '...' begin PlayFab create and join network for server
[30s] PlayFab reconnect server '...'
```

Nothing in that loop names a cause, and the parts that worked are exactly the
parts that make the real cause hard to see. **PlayFab login is managed code
over HTTPS and needs nothing special; only the relay network needs the native
runtime.** So the server authenticates, reports itself logged in, registers a
session - and then silently cannot do the one thing crossplay exists for.

`libparty.so` could not load:

```text
== /valheim/valheim_server_Data/Plugins/libparty.so
        libpulse.so.0 => not found
        libpulse-simple.so.0 => not found
        libpulse-mainloop-glib.so.0 => not found
```

PlayFab Party is a combined **voice and data** SDK shipped as one library, so it
links PulseAudio even on a headless server with no sound device and no voice
feature in use. `libatomic1` was already installed - the community's known list
for this is libpulse, libatomic1 and glibc 2.29+, and this image had two of the
three.

#### What was checked first, and was wrong

The NetworkPolicy, because it was the obvious suspect and this epoch had just
written it. It allows every port and protocol outbound to the public internet,
so it was never a candidate - established by reading it rather than by
reasoning about it, which cost one minute against a rebuild cycle.

The vendor documentation was no help either, and would not have been: this is
not a Valheim behaviour, it is a property of running Valheim in a container
built from a slim base. The answer was in two issue threads on community
container images, found by searching for the exact log line.

#### The guard is a build step, not a test

Nothing a test in this repository can reach knows what a Debian image resolves
at runtime, so the image now proves it itself: after the game files are copied,
the build runs `ldd` against `libparty.so` and fails if anything is unresolved,
naming the libraries.

That placement is the whole point. The failure it replaces produces an image
that **starts, runs, and serves a world nobody can join**, retrying forever,
with no error mentioning a library anywhere. A build that stops and says which
libraries are missing costs a minute; the version it replaces cost an evening.

Deliberately not installed: the wayland, cairo, pango and dbus libraries that
`libdecor` also reports missing. That is Unity's desktop windowing stack,
shipped in every engine build and never loaded headless - installing it would
buy megabytes of attack surface to silence a scan that is correctly reporting
something harmless.

### Six Python linters, no Python, and a required check that hung

`Analyze (Super-Linter)` normally finishes in about two minutes. Intermittently
it stopped returning and burned its full twenty-minute `timeout-minutes`
instead - on a check required to merge into `main`, so a hang was a hard block
rather than a slow lane, and it was force-merged past twice.

The last line the job ever printed was Super-Linter's own warning:

```text
[WARN] Black and Ruff are both enabled, and might conflict with each other.
```

Everything before it completed - configuration validated, file list gathered -
and nothing after it appeared. `.github/super-linter.vars` set no
`VALIDATE_PYTHON_*` at all, so all six of Super-Linter's Python linters ran by
default, against a repository where `git ls-files '*.py'` returns nothing and
always has.

**The diagnosis came from the operator, not from the log being read carefully
enough.** Two turns were spent treating this as an infrastructure flake and
tabulating durations, when the run output had already named the last thing that
happened. The instruction was blunt: "Please read the actual output I am giving
you."

That is the lesson worth more than the fix. A hang has a last line, and the last
line is evidence rather than noise. Comparing durations across runs answered
"is this abnormal", which was never in doubt, while the log answered "where did
it stop" and was sitting there the whole time.

#### And the summary it promised did not exist

Found in the same file while fixing the first thing. A comment there asserted
that Super-Linter's output "already lands in the job summary, which is where
somebody reading a failing lane is looking". It did not: the step summary was
enabled but `SAVE_SUPER_LINTER_SUMMARY` was false, which Super-Linter also warns
about on every run.

So the lane produced no summary anywhere - not as a pull request comment, by a
deliberate decision, and not in the job summary either, by omission - while a
comment in the configuration assured the reader otherwise. Both warnings had
been printing on every run for as long as the lane has existed.

#### The linter warning was not the cause, and the instrument could not work

Both halves of the section above were wrong, and the second mistake is the more
useful one.

The six Python linters were switched off, and **the hang came back**. Being last
is not being responsible: that warning prints while configuration is still being
read, so "hangs just after it" only ever meant "hangs early".

Debug logging was then added so the next hang would name the linter it stopped
in. It cannot, and could never have. Super-Linter hands every linter to GNU
`parallel`, which holds all of their output until the last one finishes - in a
passing run all ninety jobs print within the same two hundred milliseconds, at
the end - so a hung run prints nothing past the hand-off, whatever is stuck, and
the buffer dies with the container. **A buffered log's last line dates the
silence, not the fault.**

What Harden Runner actually records, read end to end for a hung run and a
passing one an hour apart: its Armour module killed and blocked nothing, it
reports no agent errors and no tampering, and the insights page holds no process
events at all at this tier - so neither source can name the stuck process. The
only refused call in any run is Checkov's startup call to `api0.prismacloud.io`,
and it is refused in the passing runs too, 115 times since the policy was
written. A change to silence that call was drafted and then held: "the one
anomaly" and "the cause" are not the same claim. The operator's words, which are
the lesson: "Shouldn't we... look at harden runner and see what it actually says
before going in blind?"

The instrument that would answer it is a snapshot taken from outside the
container - `ps -eo pid,etime,args --forest` started in the background before the
linter step and printed by an `if: always()` step, which works because a
background process outlives its step and the post-steps still run after a
timeout. It is not built; #365 carries it.

### A failed teardown left a backend file that deadlocked the estate

The worst of the run, because it broke both directions at once and named the
wrong subsystem while doing it.

After a teardown that had already succeeded, `demolish` found no local state.
That is the normal state of a sterilized workspace, but the code reads it as
"state must be where a successful ignition puts it" - in Postgres, inside the
cluster - so it copies `backend_pg.tf` into place and inits against the
database. The cluster was gone, so the init failed, the destroy halted, and
**the backend file it had just written stayed there.**

`backend_pg.tf` declares a backend for the whole module, so every later
`tofu init` in that workspace picked it up. With no `-backend-config` alongside
it, tofu fell back to dialling localhost:

```text
PHASE 2 : OVERLAY
Mint a tagged auth key for the hypervisor to join the overlay network.
  -> tofu init
Error: dial tcp [::1]:5432: connect: connection refused
```

A phase whose entire job is minting a tailnet key, failing on Postgres, which it
does not use. And the file is gitignored, so nothing about the working tree
looked wrong.

**Both routes out were closed simultaneously.** The destroy could not run
because it could not find a cluster; the rebuild could not run because the
destroy had poisoned the workspace on its way out. Neither error mentioned the
other, and the file linking them appears in no listing.

#### Two things were wrong, not one

The leak is the defect. `attachToStateInPostgres` now removes the file when the
init it wraps fails, which is the whole reason it exists as a function rather
than four inline lines, and both halves are tested.

The second is the message. It led with restoring the age-encrypted break-glass
backup, and offered "remove whatever is left in Proxmox by hand" - a manual
step this estate refuses on principle - when **the likeliest cause by far is
that there is nothing left to destroy.** A teardown that already succeeded takes
the cluster and its state together, and from inside there is no way to tell that
apart from a cluster that is merely unreachable. The message now says so, names
`task clean-secrets` as the ordinary recovery, and keeps the break-glass path
for the case that actually needs it: machines still running with no state left
to describe them.

#### The shape

An operation that writes a file speculatively owns removing it on every failure
path. This one wrote a _backend configuration_, which is the highest-blast-radius
kind of speculative file in an OpenTofu workspace - it silently redirects where
every future command believes state lives, including commands that have no
opinion about state at all.

### A secretRef naming a secret nobody creates fails closed, three layers away

The rebuild after those two fixes got the whole way to a healthy cluster - six
nodes Ready, Cilium up, all four HelmReleases installed, Postgres at 3/3 - and
then sat in the health gate waiting on Flux.

What the gate reported was `workloads-production: Source artifact not found`,
which reads as a missing tag. The tag was fine. The actual state was:

```text
flux-production   False   failed to get secret 'flux-system/flux-system':
                          secrets "flux-system" not found
```

`flux-production` carried `secretRef: {name: flux-system}` on the assumption
that the secret `flux bootstrap` creates would be present. Flux is installed
here by OpenTofu applying the manifests directly, so that secret is never
created - and the repository is public, so no credential was needed in the
first place. The `flux-system` GitRepository sitting beside it has never had a
secretRef and has always worked.

**The important property is that a missing secret does not degrade to anonymous
access.** It fails closed with `AuthenticationFailed` and the source never
builds an artifact. That is correct behaviour and it is also why one unchecked
reference was so expensive.

#### The symptom surfaced three layers from the cause

Worth writing down, because the debugging time went into the wrong places:

| Layer                  | What it said                    | What was true                                         |
| ---------------------- | ------------------------------- | ----------------------------------------------------- |
| `flux-production`      | AuthenticationFailed            | the cause                                             |
| `workloads-production` | Source artifact not found       | reads as a missing tag                                |
| `infra-configs`        | Reconciliation in progress      | `wait: true`, blocked on the GitRepository it applied |
| Health phase           | waiting for Flux reconciliation | 15 minutes, then destroy the estate                   |

`infra-configs` is the one that turns a bad reference into a lost rebuild. It
applies `flux-production.yaml` **and** has `wait: true`, so it waits for its own
GitRepository to become Ready. It cannot, so the layer never finishes, and the
ignition's health gate eventually gives up - upstream of Migrate, so the run
tears everything down.

`tests/go/repo/flux_source_secret_test.go` now refuses a `secretRef` naming a
secret nothing in this repository creates. The floor it asserts is on manifests
scanned rather than on secretRefs found, because zero secretRefs is the correct
state for a public repository and must keep passing.

### The first real rebuild failed twice, in two unrelated places

Both found by running it rather than by reading it, and both were invisible to
every check in the repository.

#### Nothing waited for the API server

The Cluster phase completed in thirty seconds and then everything that touches
Kubernetes failed at once - `terraform_data.cilium`,
`kubernetes_namespace.valheim`, `kubernetes_secret.runner_vars` and the Flux
bootstrap - all with `dial tcp 10.10.10.100:6443: connect: connection refused`.

The comment on the Cilium resource asserted the ordering was already handled:
"the kubeconfig is referenced below, which orders this after the API server
exists". That is false, and it is a good example of a comment that reads as a
fact and is actually an assumption. `talos_cluster_kubeconfig` fetches a
credential over the **Talos** API on port 50000, which answers as soon as the
cluster's PKI exists. `talos_machine_bootstrap` returns when the bootstrap RPC
is **accepted**. Both can be complete while nothing is listening on 6443,
because the apiserver, controller manager and scheduler are static pods that
still have to be pulled and started and etcd still has to elect - a couple of
minutes on a cold cluster.

So the CNI step, which is the first thing in the whole estate to touch 6443,
had nothing in front of it. The fix is a bounded poll of the apiserver's own
`/readyz` inside that step, where it can be measured, rather than an edge that
looks like it carries a meaning it does not.

**The second half of it is worse, because it was avoidable by pattern.** The
estate already has a gate for exactly this - `data.talos_cluster_health.this` -
and `gitops.tf` and both of `runner.tf`'s namespaces name it. Two resources did
not. `kubernetes_namespace.valheim` was written without it, and
`kubernetes_secret.runner_vars` wrote its namespace as the literal string
`"flux-system"`, which looks like a reference and creates no dependency
whatsoever. Ten resources were correct, two were not, and nothing could tell.

`tests/go/repo/kubernetes_gate_test.go` now refuses any `kubernetes_*` resource
that neither names the gate nor takes its namespace from a namespace resource.

#### Pool memberships raced their own machines on destroy

The teardown failed, twice, with

```text
Unable to update pool 'site0-templates': received an HTTP 500 response -
Reason: update pools failed: VM 10199 is not a pool member
```

`proxmox_pool_membership.control_plane`, `.workers` and `.untrusted` all took
`vm_id` from `each.value.vm_id` - the same number the VM resource itself reads
out of the same local. Identical value, **no dependency edge**. On destroy
OpenTofu reverses the graph, but there was no edge to reverse, so the membership
and the machine were deleted concurrently. Proxmox removes a destroyed VM from
its pool as a side effect, so whichever delete lost the race asked the API to
remove a VM that was no longer a member, and got a 500.

`.templates` and `.dmz_templates` were written the other way -
`proxmox_virtual_environment_vm.talos_template[each.key].vm_id` - and were
correct by accident of spelling rather than by decision.

**This is the "unexploded ordnance" case the destroy path exists to prevent.**
A teardown that stops half-way has already done irreversible work and leaves
machines nothing tracks, which is why it holds state and secrets rather than
sterilizing. Getting it to complete is therefore load-bearing in a way an
ordinary flake is not.

The lesson worth keeping is about the shape rather than the provider: **a value
copied from a local and a value read from a resource can be textually identical
and mean completely different things to the dependency graph.** Nothing in a
plan shows the difference, and nothing in a review does either - the diff is one
identifier. Only a destroy reveals it, which is the operation nobody runs
speculatively.

### Rendering the chart emits real private keys, and the chart says so

Found on the first render, by gitleaks, before anything was committed.

`helm template` on the Cilium chart with its defaults emits key material as
part of the output: a `cilium-ca` Secret carrying `ca.key`, and
`hubble-server-certs` carrying `tls.key`. Committing the rendered manifest -
which is the whole delivery route this epoch chose - would therefore have
published a CA private key to a public repository.

The chart labels those objects `cilium.io/helm-template-non-idempotent: "true"`
itself, so this is documented upstream rather than surprising. The second
consequence follows from that label: the keys are **regenerated on every
render**, so the committed file would show a meaningless diff every time
`task render-cni` ran, and the drift check that exists to prove the manifest
matches the pinned chart would be proving nothing.

Both problems have one cause, and it is Hubble. `hubble.enabled: false` removes
every Secret from the output - verified, zero `kind: Secret` objects remain, and
the only surviving `tls.key` strings are `optional: true` clustermesh volume
projections naming a key inside a Secret rather than carrying one.

Hubble is observability and belongs to epoch 04, so this defers it rather than
discarding it. Turning it on later is not just flipping the flag: it means
deciding where the certificates come from - cert-manager, or Cilium's own
cronJob method - because the one thing that must not happen is baking them into
git.

### Which machines get which half, and the node that does not exist yet

Asked during review, and worth answering in the record because the split is not
obvious from the diff.

**`cluster.network.cni.name` and `cluster.proxy.disabled` are set once, on the
control plane's config.** They are cluster-level facts rather than machine
ones - they decide which bootstrap manifests Talos renders, and only a control
plane renders those. This is the same convention `allowSchedulingOnControlPlanes`
already follows a few lines above, for the reason that file already gives: a
worker repeating it would be a second declaration of one fact, and two
declarations of one fact eventually disagree.

**KubePrism is machine-level, and every machine gets it.** It lives in
`local.machine_patches`, which is built from
`all_machines = merge(local.control_plane, local.workers)`, and both the
control-plane and worker configurations consume it. That is not incidental: an
agent on a worker has exactly the same problem as one on a control plane, since
without kube-proxy there is no ClusterIP anywhere in the cluster to reach the
API through.

**Workers get Cilium because the agent is a DaemonSet.** There is nothing to
configure per worker.

**The DMZ node does not exist yet, and its readiness is already decided.** It is
planned as a dedicated worker carrying a `NoSchedule` taint, with the toleration
held only by workloads in the untrusted zone. A CNI has to ignore that: a node
with no agent has no pod network, never reaches `Ready`, and never joins the
cluster - so tainting it without a tolerating CNI would produce a machine that
silently never arrives. Cilium's agent DaemonSet carries
`tolerations: [{operator: Exists}]`, which covers it, and
`TestTheCNIReachesEveryNodeIncludingTaintedOnes` now holds that in place before
the node exists to test against.

Worth naming the shape, because this repository has refused a blanket
`operator: Exists` toleration before - on an unpinned debug image proposed for a
control plane. The distinction is real rather than convenient: there, the
toleration widened where untrusted code could run; here, it is the condition on
a machine having a network at all.

**The general shape is worth keeping.** "Render a chart and commit the result"
is a reasonable pattern and this estate now uses it, but a chart is a program
and some charts generate secrets when you run them. Any future use of this
pattern checks the output for `kind: Secret` before committing, and the reason
that check exists is this one.
