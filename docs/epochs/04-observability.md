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

## Known driver: nothing watches whether the estate is on its own network

The strongest argument for this epoch, and it arrived by costing a night rather
than by being reasoned about.

The site's hypervisor left the overlay network and stayed off it for hours.
Nothing noticed. The scope above lists CPU, memory, disk pressure, etcd latency
and pod evictions - every one of which would have been perfectly healthy
throughout, because the cluster was fine. What was broken was the network the
estate uses to reach itself, and there is no signal for it anywhere.

**What the existing signals could and could not see:**

- The **Health phase** is a converge-time gate. It had passed, and by the time
  this mattered it no longer existed.
- The **estate canary** watches whether GitHub Actions is running jobs. It
  correctly reported a failed converge and could say nothing about why.
- The **vendor's admin console** showed the host as online for the entire
  outage. It reports registration with the coordination server, not whether any
  traffic can pass.

That last one is the finding worth carrying into this epoch's design.
**Registration is not reachability**, and every cheap way to check a mesh -
querying the vendor's API, reading a device list, watching a daemon's own
`active (running)` status - reports the first while the question is always the
second. A monitor built on any of them would have been green throughout.

**What the shape of the fault implies for the check.** Every peer pair
involving the hypervisor failed and every pair not involving it succeeded. That
signature localises the fault immediately, and it is only visible **pairwise**:
a star check against one hub would have reported the mesh broken and pointed at
nothing. As sites and hypervisors are added the pairs grow quadratically, so
the set has to be derived from the site configuration rather than listed, and
each vantage point has to report its own row.

**Where it can run.** Only a member of the overlay can measure it, which means
the hypervisors rather than the workstation - the workstation is not a member.
But a monitor inside the estate cannot report that the estate is down, which is
precisely what the canary already exists to do from outside without holding any
credential that reaches in. So the division is: peers measure and report, and
the canary notices when reports stop arriving.

`scripts/survey` is the first increment of this. It runs on a peer, probes
every peer it can see rather than trusting their status, and reports one row of
the matrix; run on each hypervisor, the rows assemble into the whole. This
epoch owns making it periodic, making its output land somewhere durable, and
deciding what a hole in the mesh should page for.

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
