# Epoch 05 — Node Lifecycle

- **Tier / path:** `management/`, `scripts/steward/`
- **Branch:** `epoch/05-node-lifecycle`
- **PR:** #
- **Status:** Not started

## Goal

Replace one control-plane node without rebuilding the cluster. At the end,
changing a node - its image, its size, the hypervisor it sits on, or the fact
that it died - is an ordinary operation the estate performs on itself, one
machine at a time, with quorum intact throughout.

Its own epoch because it is a core function rather than a feature. A control
plane whose nodes cannot be replaced individually is one that can only be
rebuilt, and rebuilding is not a maintenance procedure - it is an outage with a
plan attached. Kubernetes exists in large part to make node replacement
unremarkable; an estate that cannot do it has given that up while still paying
for the complexity.

## Scope

In scope:

- A driver that rolls one node at a time: create the replacement, wait for it
  Ready and etcd healthy at the new membership, cordon and drain the old one,
  `talosctl reset`, remove the etcd member, move on.
- Refusing to start, or to continue, when the cluster is already degraded.
- The operations this unlocks, each of which is currently a rebuild: a Talos
  version or schematic change, a CPU or memory resize, moving a node to a
  different hypervisor, and replacing a node whose hardware is gone.
- Whatever `steward` verb or phase expresses it, and how a converge decides
  between an in-place change and a roll.

Explicitly out of scope:

- **Autoscaling.** `control_plane_count` is a provisioning input and etcd
  quorum is fixed at creation - epoch 01 and `02-abstraction.md` both say so,
  and rolling replacement does not change it. This epoch makes a deliberate
  change safe, not an automatic one possible.
- **Workload disruption budgets and application-level draining** - epoch 03
  owns what runs on the platform.
- **Worker nodes.** There are none yet. The design should not preclude them.

## Decisions

_Recorded as they are made. Two are already settled by work done in epoch 01._

### Identity is the machine, already

`for_each` keyed by host octet landed in epoch 01, so adding or removing a
single node is already one create or one destroy rather than a renumbering of
everything after it. That was the precondition; without it, no amount of
ordering would have helped, because OpenTofu would still have planned five
replacements at once.

### Add before removing, and never sit even for long

**Chose:** grow to N+1, wait for health, then shrink to N.
**Because:** removing first takes a five-member cluster to four, where quorum
is still three and the tolerance for a second failure is gone precisely while
one node is deliberately being disturbed. Growing first costs a transient even
membership, which is tolerable for minutes and intolerable as a resting state.
**Rejected:** replace-in-place, which is what a naive `terraform apply` does
and what epoch 01's placement re-deal hazard already describes going wrong.

## Deferred

- **Placement is still a re-deal.** `vm_placement` recomputes
  `i % length(hypervisors)`, so adding a hypervisor reassigns existing nodes -
  the hazard recorded in `02-abstraction.md`. Keying by host octet fixed
  identity churn, not placement churn. Fixing it needs the assignment recorded
  rather than derived, and it becomes urgent the moment a second hypervisor
  exists. Trigger: a second hypervisor, or this epoch, whichever comes first.

## Gotchas

- **The runner lives on the cluster it would be rolling.** A roll that disturbs
  the node hosting the ARC listener kills the job driving it, halfway through.
  Either the driver has to be re-entrant - safe to re-run and able to work out
  where it stopped - or a management roll runs from the workstation rather than
  from CI. That is a design constraint, not an afterthought.
- **Observability is the prerequisite this epoch actually has.** "Wait until
  healthy" is only as good as what can be measured, and epoch 04 is what makes
  node pressure, etcd latency and eviction visible. Rolling blind is how a roll
  that is technically succeeding takes a cluster down slowly.
