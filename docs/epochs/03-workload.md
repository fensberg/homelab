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
2. **The machine.** Remove the site's `dmz_count` (or lower it), then
   `contractor converge`. A machine in this zone is not an etcd member, so this
   is a drain and one destroy rather than a quorum event, and it renumbers
   nothing else.
3. **The port forward.** On the router, by hand. Nothing in this repository can
   see it, which is the reason it is listed here at all.
4. **The network**, below.

Doing this in the other order takes a workload's network away while the
workload is still running, which is a diagnosis nobody enjoys.

#### What `dmz_count: 0` does on its own

Everything derived from the machines stops existing: no VM, and no second
image, because `proxmox_download_file.dmz_disk_image` keys off
`local.dmz_hypervisors` rather than off the site. The SDN tasks are gated on
the same count, so nothing new is created.

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

- the `vnetdmz` vnet
- its subnet
- the second Talos image in `local-iso`

Clearing them is a hypervisor operation, listed before deleted because the
subnet id is generated and should be read rather than guessed:

```sh
# What is actually there
pvesh get /cluster/sdn/vnets/vnetdmz/subnets --output-format json
pvesm list local-iso | grep '/dmz-'

# Remove, innermost first, then apply the SDN change
pvesh delete /cluster/sdn/vnets/vnetdmz/subnets/<id from the listing>
pvesh delete /cluster/sdn/vnets/vnetdmz
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

**The zone's addressing stays too.** `dmz_cidr`, the gateway and the VNI are
derived from the site's octet rather than chosen, so they cost nothing while
unused and they are the same values if a zone ever comes back. Deleting them
would be deleting arithmetic.

## Outcome

## Deferred

## Gotchas

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
