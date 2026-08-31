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

## Deferred

- **Placement is still a re-deal.** `vm_placement` recomputes
  `i % length(hypervisors)`, so adding a hypervisor reassigns existing nodes -
  the hazard in `02-abstraction.md`. Adopting Cluster API would retire the
  expression entirely, which is one more argument for evaluating it before
  patching it. Trigger: a second hypervisor, or this epoch.

## Gotchas

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
