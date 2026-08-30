# Epoch 04 — Observability

- **Tier / path:** `clusters/management/infrastructure/`
- **Branch:** `epoch/04-observability`
- **PR:** #
- **Status:** Not started

## Goal

Know what the estate is doing between runs. Today the only signals are the
Health phase, which is a point-in-time gate during a converge and then stops
existing, and CI failing. Nothing watches CPU, memory, disk pressure or pod
evictions in the hours and weeks in between. At the end of this epoch there is
a written answer to "is anything about to fall over" and to "when do we add a
node", and both are answerable without anyone logging in.

Its own epoch rather than a section of another one, because it is a platform
concern that outlives ignition and grows with every workload added after it.
Folding it into `management/` would make it a side effect of the epoch that
happens to have built the cluster it runs on.

## Scope

In scope:

- Metrics for the layer this estate actually owns: node CPU, memory, disk and
  pressure per control-plane node; etcd health and latency; pod restarts,
  evictions and OOM kills; PersistentVolume capacity.
- A small number of alerts that mean something, delivered somewhere a human
  reads.
- **Written scaling thresholds**, in this record, expressed against
  `control_plane_count` - the lever epoch 01 already built.
- Retention sized for a homelab: enough history to see a trend, not enough to
  need its own storage epoch.

Explicitly out of scope:

- Application-level and business metrics for workloads - epoch 03 owns what it
  deploys, and this epoch owns the platform underneath.
- Log aggregation. It is a different problem with a different cost profile and
  deserves its own decision rather than arriving as a free extra.
- Distributed tracing. There is nothing here yet that would benefit.

## Decisions

_Recorded as they are made._

### Metrics without thresholds is a wall of graphs

**Chose:** every metric this epoch collects has to answer a question somebody
has actually asked, and the scaling thresholds get written down here rather
than left to judgement.
**Because:** the estate already has the actuator - change
`control_plane_count`, merge, converge - and no sensor. Adding the sensor
without deciding what reading means "act" produces dashboards nobody opens and
a decision still made at 2am by whoever is awake. A threshold in this file is
reviewable, arguable and testable in a way a feeling about a graph is not.
**Rejected:** dashboards first, alerts later. That ordering is how monitoring
becomes decoration.

## Deferred

- **Log aggregation**, per Scope above. Trigger: the first incident where
  metrics said something was wrong and nothing said what.
- **Alerting beyond email.** GitHub already emails on a failed workflow, which
  is the alerting the backup story leans on; anything richer is worth having
  only once there is more than one person to route to.

## Gotchas

- **Headroom here is genuinely tight, and this epoch is what will show it.**
  Five control-plane nodes at 4 cores and 4GiB each already run etcd, the API
  server, three CloudNativePG instances, Flux, OpenEBS and now ARC runner pods
  - and epoch 01's record anticipates moving eight CI lanes onto the same
    nodes. The first symptom of over-subscription there is not a dashboard, it
    is etcd getting slow and the control plane becoming intermittently unwell,
    which is miserable to diagnose without exactly the metrics this epoch adds.
    Monitoring is a prerequisite for that CI migration, not a follow-up to it.
- **`cloudnative-pg.yaml` already has `podMonitorEnabled: false`**, with a
  comment saying to turn it on once Prometheus exists. That flag is the first
  thing this epoch flips, and a good check that the stack really is scraping.
