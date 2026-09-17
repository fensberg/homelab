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

`scripts/contractor/internal/survey` is the first increment of this. It runs on a peer, probes
every peer it can see rather than trusting their status, and reports one row of
the matrix; run on each hypervisor, the rows assemble into the whole. This
epoch owns making it periodic, making its output land somewhere durable, and
deciding what a hole in the mesh should page for.

## Known driver: three placement questions that are alerts, not tests

Arrived from epoch 02, where the first placement audit of the live cluster
found them and the first instinct was to write them as integration tests. The
operator refused that framing, and the line they drew is the one to keep:
**"isn't the discovery of processes on which node the job of a monitor? Why
test for it?"**

The distinction generalises past these three. **A test compares reality against
something the repository declared** - when it fails, a file is wrong or was
silently not applied, and somebody fixes it in a commit. **A monitor watches
state that nothing declared** - a scheduler's runtime choice, an upstream
component changing - and when that moves there is nothing in the repository to
fix. The practical test is whether a red result sends someone to a file or to
the cluster. If it is the cluster, it is an alert.

Failing an acceptance run for the second kind is not merely untidy: it is a
guard broken toward noise, which is the direction that gets guards switched off.
A red integration run that means "the scheduler put two pods together" teaches
everybody to skim red integration runs.

What stayed in `tests/go/integration/placement_test.go` is the part that is a
declaration: the workloads this repository deploys, and whether the placement,
requests and priority class they declare actually arrived. Those are set
through Helm values, a value at a path the chart does not read is accepted in
silence, and the deployed pod then disagrees with the manifest. That is drift
between code and reality, the same question `TestDeployedEstateMatchesTheCode`
already asks of OpenTofu.

Three things came out of the suite and belong here instead.

**Replica spread within a Deployment.** Both CoreDNS replicas run on one node
(#274) and have since the cluster was built. Nothing in this repository asks
otherwise - Talos's rendered Deployment carries a `podAntiAffinity` at
`preferredDuringSchedulingIgnoredDuringExecution`, and `preferred` plus
`IgnoredDuringExecution` means the scheduler co-located them at bootstrap, when
one node was Ready, and has never revisited it. No commit fixes that, which is
exactly why it is not a test. The alert is "a Deployment with more than one
replica has them all on one node", and it generalises past CoreDNS to anything
this estate later runs in pairs.

**Anything in the namespace Talos owns going BestEffort or unclassified.** The
integration tier now skips `kube-system` entirely, and that exclusion was
argued against before it was made - it hides a regression in Talos's own
components as readily as it hides the known kube-proxy case. The objection is
right and the conclusion was wrong: such a regression is worth being told about
promptly, not worth failing an acceptance run over, and a Talos upgrade
changing a component's QoS class is precisely a thing the world did rather than
a thing this repository got wrong.

**Requested against actually used.** The audit could only report requests -
what the scheduler has promised. The two diverge in both directions: kube-proxy
requests zero and uses something, and the API server reserves 512 MiB whether
or not it wants it. Every sizing number in `02-abstraction.md` is a judgement
awaiting this measurement, and the operator's standing goal - near 100%
utilisation, never pegged - cannot be evaluated at all without it. This is the
single most load-bearing thing this epoch adds.

A fourth, noticed while answering a question about priority classes rather than
by the audit: **three pods on the workers carry a priority this estate did not
choose.** Flux's source, kustomize and helm controllers ship with
`system-cluster-critical` from upstream, which is 2,000,000,000 - two thousand
times the top of this estate's own scale, and above `critical`. It is probably
right that Flux outranks everything here, but it was upstream's decision rather
than one made in `priority-classes.yaml`, and the eviction order on a full
worker depends on it. Worth surfacing rather than deciding now.

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

### The deliverable is a panel schedule, not a dashboard

**Chose:** this epoch produces a written capacity budget in the shape an
electrician's panel schedule takes - total allocatable per node, what is
reserved, what may burst, and what the worst-case simultaneous draw is - and
the scaling thresholds hang off it.
**Because:** [`02-abstraction.md`](02-abstraction.md) adopts a domestic
electrical panel as the estate's model for capacity, and the model comes with
its own deliverable. The reason electrical code permits 60A of appliances on a
40A feed is the **demand factor**: a claim that they will not all peak together.
Requests set below limits is that same bet, and a bet has to be checked.

**So this epoch is the precondition on filling the box on purpose.** You cannot
safely overcommit what you cannot measure - the demand factor is unverified
until something has watched the peaks for long enough to say whether the claim
holds. That is a third independent argument arriving at this epoch, alongside
the overlay outage above and the node-pressure gotcha below.

Two things fall out of it that a graph does not give:

- **A trip is distinguishable from a busy estate.** The panel model says the
  fuse is memory alone - CPU degrades rather than fails - so an alert on CPU
  saturation is noise and an alert on memory headroom is the one that means
  act. Recording that here stops both being wired up as "resource usage".
- **The thresholds have a denominator.** "Add a node when X" is arguable in a
  way "the graph looks high" is not, and the schedule is what X is expressed
  against.

### What this epoch deploys, and where each answer goes (agreed 2026-09-17)

Four questions were settled with the operator before anything was built.

**The stack is kube-prometheus-stack, with Grafana.** Prometheus, Alertmanager,
node-exporter, kube-state-metrics and the operator, plus metrics-server beside
it so `kubectl top` answers at all.

The operator asked the right question about that chart source - why not take
Prometheus from the Prometheus project - and the answer is that **the project
publishes no chart**. It publishes the server, Alertmanager and node-exporter,
as images on quay.io. The charts live in `prometheus-community`, "Prometheus
Monitoring Community Projects", which is under the umbrella and explicitly not
the core team; the operator lives in `prometheus-operator`, whose own
description says it "is an independent project from the Prometheus project".
So there is no straight-to-source option for a Kubernetes deployment, only
three assemblies: this chart, the operator's own jsonnet (a toolchain this
repository does not have), or writing every manifest here and owning upstream's
RBAC and scrape configuration forever - which also drops the operator and makes
`podMonitorEnabled` meaningless. The chart is the same shape every other
component here already arrives in.

**metrics-server does not come from a chart.** kubernetes-sigs publishes a
single `components.yaml` release manifest beside it, and one Deployment plus an
APIService needs no Helm indirection. Taken that way, pinned by digest from
registry.k8s.io, which is already an approved registry - so this epoch adds one
chart source rather than two. The operator's CRDs are what
`cloudnative-pg.yaml`'s `podMonitorEnabled: false` has been waiting for.
Grafana was the arguable half - the decision above says the deliverable is a
panel schedule rather than a dashboard, and a dashboard tool invites the
opposite - and it is taken anyway because the load-bearing measurement of this
epoch, requested against actually used, is an exploration before it is a
threshold. The discipline stays where it was: the thresholds get written into
this record, and Grafana is where the question is asked rather than where the
answer lives.

**Prometheus gets a 20 GiB volume, and that is bounded by the disk that
exists.** Each worker carries a 32 GiB data disk at `/var/mnt/storage`, which
is the whole pool `openebs-hostpath` hands out of, already shared with the
state database. Fifteen days of a cluster this size is 2-5 GiB - roughly 15-20k
active series at a sample every 15 seconds, at about two bytes a sample - so
20 GiB is four to five times the estimate and still leaves the disk room. The
operator asked for 100 GiB on the "go big, then scale down" principle; it does
not fit without growing every worker's disk through a converge, and a hostpath
volume cannot be expanded in place anyway, so starting big costs the same
recreation later that starting modest does. `retentionSize` is set just under
the volume, so Prometheus evicts rather than filling a disk the database is
also writing to.

**Grafana comes from Docker Hub, because it comes from nowhere else.** Probed:
`docker.io/grafana/grafana` answers and the same path on ghcr.io and quay.io
does not. That needed a supplier decision rather than a shrug, because the
docker.io entry approved Docker Official Images for build bases and asserted
that nothing in the cluster pulls from there at runtime - which was already
untrue, since OpenEBS' provisioner does, and it is the component handing out
every volume. Grafana Labs is now its own entry beside the Official Images one,
so it can be removed on its own, and the false sentence is corrected rather
than left standing. Mirroring the image into this estate's registry was
considered and deferred: a new mechanism to re-run on every bump, which would
not change OpenEBS' dependency on the same registry anyway.

**Cost, stated before it is spent.** The control planes are 4 cores and 4 GiB;
the workers offer about 7.2 GiB allocatable each and run at 35-39% of it. The
stack is roughly 1.2 GiB with Grafana, on the workers. That is affordable and
it is also the first thing the new measurements will judge - if the panel
schedule says this is the wrong tenant for this estate, that is a finding
rather than an embarrassment.

**An alert becomes a GitHub issue.** A scheduled job on the self-hosted runner
asks Prometheus what is firing, opens one issue per alert, and closes it when
the alert clears - so notification is GitHub's job, which already reaches the
operator, and a firing alert cannot be scrolled past. No new vendor, no
credential the estate does not already hold, and no inbound path.

Google Chat was the operator's preference and is **not available**: incoming
webhooks require a Business or Enterprise Workspace account and an
administrator setting, which this estate does not have. Worth recording what
was learned in case that changes, because it shapes the adapter rather than
just the destination: the webhook URL carries `key` and `token` query
parameters and **is itself the whole credential**, there is no auth header, the
quota is one request per second per space shared by every webhook in it, a
message is capped at 32,000 bytes, and `threadKey` is what keeps every firing
of one alert in a single thread. The delivery step is therefore written as a
formatter and a destination rather than as "open an issue", so Chat, Discord or
email later is a URL and a shape, not a redesign.

**The mesh check is node-exporter's textfile collector.** A timer runs
`contractor survey` on each hypervisor and writes its row of the pairwise
matrix where node-exporter publishes it; Prometheus scrapes the hypervisors
over the overlay. One mechanism gives both the matrix this epoch's first driver
demands and host metrics for the hypervisors, which nothing watches today. A
Pushgateway was rejected: a stale push and a fresh one look identical unless
something checks a timestamp, and the fault being watched for is exactly the
one that stops pushes arriving.

**Grafana is reached through Cloudflare Tunnel, behind Cloudflare Access.**
The operator chose the tunnel over an overlay-only service. Two consequences,
recorded because they were accepted rather than discovered: it builds the
tunnel epoch 03 wants for the website, so part of that epoch lands inside this
one; and Access goes in front, because the alternative is Grafana's own login
page on the public internet. Cloudflare is already an approved supplier.

**The tunnel comes early, not late.** The operator asked for Cloudflare Tunnel
sooner rather than later, so it lands with the deployment rather than after the
thresholds - Grafana is reachable the day it exists, and epoch 03 inherits a
tunnel that has been carrying something real.

**The work lands in three pull requests**, in this order: the chart suppliers,
with the justification for each; the stack deployed with short retention and no
alerts, so it starts gathering the requested-against-used data; then the panel
schedule and the thresholds, with alerts wired to them once there is a week of
data to write them from. The middle step is deliberately a measurement rather
than a configuration: every threshold in this record is meant to have a
denominator taken from this estate rather than from an article about somebody
else's.

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
