# Epoch 05 — Node Lifecycle

- **Tier / path:** `management/`, `clusters/management/`
- **Branch:** `epoch/05-node-lifecycle`
- **PR:** #
- **Status:** Not started

## Goal

Replace a control-plane node without rebuilding the cluster, using the
mechanisms that already exist for it. At the end, changing a node - its image,
its size, the hypervisor it sits on, or the fact that it died - is an ordinary
operation, one machine at a time, with quorum intact throughout.

Its own epoch because it is a core function rather than a feature. A control
plane whose nodes cannot be replaced individually can only be rebuilt, and
rebuilding is not a maintenance procedure - it is an outage with a plan
attached.

## Acceptance test: change 5 to 3 and watch it land

Hard criterion. This epoch does not close without it:

> Change `control_plane_count` from `5` to `3` in the config. **Merge it.**
> Two machines are cordoned, drained, removed from etcd and destroyed - with
> nobody having run anything, and the cluster healthy throughout.

It sits here rather than in epoch 01 because removing a node is a different
problem from adding one, and only the removal needs the machinery this epoch
builds:

- A **cordon and drain** that honours PodDisruptionBudgets, so work moves
  before its machine goes.
- An **etcd member removal**, because destroying the VM leaves the member
  registered and etcd retrying against a machine that no longer exists.
- A guarantee that **the machine being destroyed is not running the thing
  destroying it**. The self-hosted runner lives on these nodes; a converge that
  deletes its own node terminates mid-apply, holding a state lock, with the
  estate half-changed.

Adding a node needs none of the three, which is why epoch 01 can prove the
merge-driven path in the up direction now and this epoch owns the down
direction.

Note this is ordinary practice rather than novel work - Cluster API with the
Proxmox provider does all three, which is why the decision below is to adopt it
rather than write a driver.

## Scope

**This epoch adopts and integrates. It does not build a node controller.**

That is worth stating first because the first draft of this record got it
wrong: it scoped a bespoke driver to create a node, wait, cordon, drain, reset,
remove the etcd member and repeat. Every one of those steps already exists,
several of them in tools this repository already runs, and one of them -
Cluster API with the Proxmox infrastructure provider - is named as "the genuine
answer" in [`02-abstraction.md`](02-abstraction.md).

In scope:

- **Evaluate Cluster API** with the Proxmox infrastructure provider and the
  Talos bootstrap and control-plane providers. `TalosControlPlane` performs
  rolling control-plane replacement, including etcd membership, and
  `MachineHealthCheck` remediates a node that has failed. This is the piece
  that would actually replace hand-written OpenTofu.
- **Adopt `talosctl upgrade`** for Talos version and schematic changes, which
  are in-place and node-by-node and need no VM replacement at all.
  `talosctl upgrade-k8s` covers the Kubernetes components.
- **PodDisruptionBudgets** on anything that matters, so that `drain` is a
  correctness boundary rather than a hope. The state database is the first
  candidate: CloudNativePG ships one.
- Whatever remains that nothing else covers, once the above is real. That list
  is expected to be short, and writing it is part of the epoch.

Explicitly out of scope:

- **Autoscaling** - `control_plane_count` is a provisioning input and etcd
  quorum is fixed at creation. Unchanged by any of this.
- **A worker pool**, which `02-abstraction.md` already owns and which is the
  prerequisite for most of the interesting cases.

## Decisions

### Adopt, do not build

**Chose:** evaluate and integrate existing mechanisms.
**Because:** node replacement is one of the most thoroughly solved problems in
this ecosystem. Cordon and drain, honouring PodDisruptionBudgets, is how work
is moved off a machine; Talos manages etcd membership and upgrades itself in
place; Cluster API turns "replace this machine" into a declarative object with
surge, health checks and remediation. Reimplementing that in Go would produce a
worse version of a thing that already exists, and would then need maintaining.
**Rejected:** a bespoke roll driver, which is what the first draft of this
record proposed.

### A failed converge does not get to pull this epoch forward

A converge that fails partway through applying leaves machines the config no
longer describes, and the obvious recovery - revert the change and converge
onto the revert - is a **scale-down**. So the rollback path is a second customer
for this epoch, and the question was asked directly: build cordon, drain,
PodDisruptionBudgets and etcd member removal now, so a failure can recover
itself?

**Chose:** no. A failed converge destroys only the machines it created that
never joined the cluster, and anything that did join is refused with a pointer
to this epoch.
**Rejected:** building the removal machinery inside epoch 01 to serve the
rollback case.
**Because:** three reasons, in increasing order of how much they matter.

The pieces named in the question are not the work. `kubectl cordon`, a drain
that respects PodDisruptionBudgets, and `talosctl etcd remove-member` are all
one-liners over tools already here. The work is making a config change _mean_
that sequence, in order, before OpenTofu destroys the VM - which is what a
machine controller is, and why the decision above is to adopt one rather than
write it.

An unattended rollback is the worst possible debut for a destructive
capability. It would first execute during a failure, on an estate already in a
state nobody has looked at, with no human watching. The first exercise of node
removal should be the deliberate, watched `5 -> 3` that is this epoch's
acceptance criterion.

And the rollback is the _lesser_ customer. #97 - a Talos image or schematic
change cannot reach a running estate - needs the same machinery, is a permanent
functional gap rather than a rare recovery, and is what should shape the
design. Building lifecycle to serve the rollback case would shape it around the
rarer requirement and then need reshaping for the real one.

**The tie is fail-closed rather than documentary.** The narrow destroy refuses
any machine that joined the cluster, and the refusal names this epoch. A note
saying "revisit in epoch 05" can be forgotten; a refusal announces itself the
first time somebody needs more than the narrow version does.

**What is and is not tech debt here**, stated honestly rather than
generously. Destroying a machine that never joined is permanently correct -
Cluster API would do the same, and adopting it does not make that wrong. The
debt is the hand-rolled answer to "did this machine join", which a machine
controller owns as a first-class fact rather than something inferred by asking
etcd.

### Several operations turn out not to need replacement at all

Written down because the first draft listed them as rebuilds and they are not.
A Talos version or schematic change is an in-place `talosctl upgrade`, node by
node. A Kubernetes version change is `talosctl upgrade-k8s`. Those are the two
most frequent node-level changes an estate actually makes, and neither is a
lifecycle problem. What genuinely needs replacement is narrower: hardware that
has gone, a move between hypervisors, and a resize the hypervisor cannot do
live.

### Add before removing, and never sit even for long

**Chose:** grow to N+1, wait for health, then shrink to N - which is what
Cluster API's surge behaviour does by default.
**Because:** removing first takes a five-member cluster to four, where quorum
is still three and the tolerance for a second failure is gone precisely while
one node is deliberately being disturbed.

### Identity is the machine, already

`for_each` keyed by host octet landed in epoch 01, so a single-node change is
expressible rather than being a renumbering of everything after it. That
remains the precondition for any of this, whichever mechanism drives it.

### Rejected for now: workers that scale to zero

**Chose:** a permanent worker floor, with elasticity deferred until something
would actually receive the capacity it frees.
**Rejected:** Cluster Autoscaler scaling a worker `MachineDeployment` from zero,
so that no workers exist until a job is queued.
**Because:** the mechanism is real - CA supports scale-from-zero and CAPI with
the Proxmox provider would create the machine - but on this estate nobody
receives what it releases.

In a cloud, scale-to-zero is a billing mechanism: the machine stops being
rented. Here there is no bill. The hypervisor is single-tenant, its memory is
paid for whether or not a VM holds it, and the power difference between an idle
VM and no VM is inside the noise. [`02-abstraction.md`](02-abstraction.md)
already makes half this point - "what autoscaling buys on bare metal is better
packing and faster response to load, not elasticity" - and with one tenant even
the packing argument is empty.

**What it would cost is measured rather than assumed.** Epoch 01's merge-driven
scale-up put two new nodes at nineteen minutes old and Ready. CAPI-driven
creation would be considerably faster than a full converge, but it is still a
Proxmox clone, a Talos boot, a config apply, a cluster join and then a cold
containerd cache pulling the runner image with no layers on disk. Minutes, not
seconds - paid by every lane in a cold burst, including the 40-second ones. CA's
default scale-down delay is ten minutes, so a push arriving shortly after the
previous one catches the pool mid-teardown and pays it again.

**The layer that should scale to zero already does.** The runner scale set sets
`minRunners: 0`: the listener watches the queue and creates ephemeral runner
pods per job. "Spins up when work arrives" is already true where it costs
seconds. The question was only whether the machine underneath also disappears,
and that is the layer where it costs minutes and buys nothing.

**Two honest qualifications**, so this is not read as the capability being hard.

Worker removal is genuinely smaller than this epoch's acceptance test. Two of
the three hard requirements above - etcd member removal, and not destroying the
machine running the destroyer - are control-plane problems. **Workers are not
etcd members.** Removing one is a cordon, a drain honouring
PodDisruptionBudgets, and a destroy. Unbuilt rather than difficult.

And the sequencing objection is this record's own: an unattended rollback was
rejected as the worst possible debut for a destructive capability. Scale-to-zero
makes node destruction the routine hot path, firing on CI's schedule dozens of
times a day, before it has been performed once deliberately with somebody
watching. Same objection, different trigger.

**The trigger that would revisit this:** a second tenant genuinely competing for
the hypervisor's memory - the epoch 03 workloads, or a second site sharing
hardware - so that capacity released by a departing worker goes somewhere rather
than back into an idle pool. Until then the worker floor stays warm, and the
first worker holds the container and tool caches that make CI fast.

## Deferred

- **Placement is still a re-deal.** `vm_placement` recomputes
  `i % length(hypervisors)`, so adding a hypervisor reassigns existing nodes -
  the hazard in `02-abstraction.md`. Adopting Cluster API would retire the
  expression entirely, which is one more argument for evaluating it before
  patching it. Trigger: a second hypervisor, or this epoch.

## Gotchas

### An image change cannot be delivered by a converge

`proxmox_download_file` is identified by its datastore path, which carries the
Talos version but not the schematic id. Re-minting a schematic therefore
produces no plan diff, the new image is never fetched, and the nodes never
rebuild - confirmed by a real plan against a live estate that showed only a
tailnet key being replaced.

This means no Talos version bump and no extension change can reach a running
estate today; the only way to deliver one is to tear the estate down and ignite
it again. That is the same shape as nothing removing an etcd member: an
operation everybody assumes is supported, which is silently not.

Whatever this epoch adopts has to own image rollout as well as node count, and
Cluster API's machine templates are exactly that mechanism - replacing a
machine when its template changes is the thing it does.

- **Cluster API needs a management cluster, and here it would manage its own.**
  The controllers have to run somewhere, and the only cluster is the one being
  managed. `clusterctl move` and a self-hosted pivot are the established
  answers, and the circularity is the same one epoch 01 is built around: the
  thing that creates the cluster cannot depend on the cluster. Expect this to
  be the hardest part of the evaluation, not the provider.
- **The Proxmox infrastructure provider is young.** Worth weighing honestly
  against hand-written OpenTofu that works today, and worth a real look at what
  breaks when it lags a Proxmox release.
- **Adopting CAPI is a rewrite, not an addition.** It would replace most of
  `compute.tf` and `talos.tf`. That is a legitimate reason to phase it, and not
  a reason to build something bespoke instead.
- **Observability first.** "Wait until healthy" is only as good as what can be
  measured, and epoch 04 is what makes node pressure, etcd latency and eviction
  visible. Rolling blind is how a roll that is technically succeeding takes a
  cluster down slowly.
