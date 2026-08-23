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
site is an instantiation rather than a `TF_VAR_site_index` switch.

### The unit of addressing is the site, not the hypervisor

A subnet per hypervisor node is the wrong split. Nodes in one Proxmox cluster
must share a subnet, because a single Talos cluster spanning them needs its
members on one network. Give each *site* a /16 and subnet within it:

| Range | Holds |
| --- | --- |
| `10.<site>.0.0/24` | hypervisor and infrastructure |
| `10.<site>.10.0/24` | Talos cluster nodes |
| `10.<site>.20.0/24` | load-balancer pool for workloads |
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
  running. A stretched cluster loses quorum and goes down at *both* ends.
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

### One cluster per site, not one cluster across sites

A single Talos cluster with three nodes at each of two sites is a stretched
cluster, and it should be avoided. etcd raft is latency-sensitive - members
want single-digit millisecond round trips, and an overlay-network link between
sites will not deliver that. A partition between sites also leaves six members
with no tiebreaker.

The management-tier and workload-tier split this repo already describes is the
right shape: one cluster per site, all reconciled from one git repository by
Flux. Scale by adding clusters, not by stretching one.

## Open questions to settle first

- Which epoch-01 resources genuinely want to be modules, versus staying
  single-use in `management/cluster`?
- Module versioning: relative path in-repo, or tagged and pinned?
- Does the self-hosted runner have what these modules need at apply time?
  `deploy-infrastructure.yml` path-filters on `modules/infrastructure/**`, so
  a change here triggers a real apply.

## Decisions

_Record as made._

## Outcome

## Deferred

## Gotchas
