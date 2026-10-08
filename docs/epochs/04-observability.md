# Epoch 04 — Observability

- **Tier / path:** `clusters/management/infrastructure/`
- **Branch:** `epoch/04-observability`
- **PR:** #
- **Status:** In progress

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

## Acceptance criteria

Agreed 2026-10-03. The epoch does not close until each is true and something
checks it. They replace "a written answer to when do we add a node" with the
thing that answer was for: a site that is run full on purpose, and knows it.

**The aim they serve.** The estate runs lean. A host's memory is handed out
in full and the work on it is packed past what would fit if everything peaked
at once - 110% - on the bet that it will not. Monitoring is what checks the
bet, and node lifecycle ([`05-node-lifecycle.md`](05-node-lifecycle.md)) is
what acts on the answer.

Capacity is read, never typed:

- **1. The host is measured.** Prometheus holds each hypervisor's memory,
  memory pressure, disk use and disk latency, and a test finds a healthy
  target for every one.
- **2. A site's capacity comes from its hardware.** What the host has is asked
  of the host. Taken off it, each also read and not assumed: the
  hypervisor's own share, its filesystem cache, anything on it that is not
  the site's, and a reserve of one control plane to roll with.
- **3. No machine's size or count is a literal** (#615, #566). Both are derived
  from the capacity above, the overhead each machine carries, and the
  largest single thing that must fit on one - and then rounded down to a
  step on a short ladder of standard sizes, whole processors and round
  amounts of memory, so a machine is "6 and 12" and never a fraction. The
  count of workers is derived once applications say what they need (10).
- **4. The as-built record carries the hardware's facts**, so a pull request's
  plan can use them with no credential.

The budget, and who refuses:

- **5. The hypervisor is strict.** The machines' memory never adds up to more
  than the host's capacity. A plan that would is refused.
- **6. Kubernetes is where the site is overfilled.** Everything that is not
  `batch` fits in reservations with one worker gone. Work that can wait is
  not counted against capacity: it runs in whatever is free, the idle part
  of another's reservation included, and is stopped and retried when that
  is wanted back, with a message that says so. There is no cap on the
  total. A pull request that leaves must-run work with no room is refused
  by its plan, which names the shortfall. Disk is held to a budget as
  memory is.
- **7. A pod with no reservation or no priority class is refused by the
  cluster**, whoever created it, and each namespace has a quota.
- **8. etcd's disk latency has a written level** and an alert on it (#627),
  and either etcd has a disk of its own or that level gates what is added.

Sizing needs nobody:

- **9. A workload's reservation follows its measured use**, applied by the
  autoscaler with no pull request, inside a floor and a ceiling per
  container, in place where the cluster can, and never past a disruption
  budget. Use means use under load: a size is taken from what a thing
  needed while it was doing its work, over a long window, grows quickly
  and shrinks slowly, and is never taken from one reading or from a thing
  that sat idle. Until load has been seen, what was declared stands. The
  hypervisor's own filesystem cache is sized by the same rule, from a
  default. etcd, the API server and the databases are sized by declaration
  until there is a reason to trust a guess about them.
- **10. An application says what it expects to need before it has run**, with
  where the figure came from, and the plan counts that until a measurement
  replaces it. What it needs is its manifest's reservation, which is the
  declaration; where that came from is data beside it (`sized_from`), one
  of the publisher's figure, an estimate, or measured over a named window
  under a named load, and the core says the same of its own. Built; the
  plan does not count them yet (6).

What is measured is looked at:

- **15. Each question this epoch asks has a dashboard**, kept in git:
  capacity by scope (hypervisor, machine, workload), what is reserved
  against what is used, and whether the network is what limits the site -
  traffic against each interface's own speed, with its errors and drops.

History outlives the site:

- **11. A site's history lives as long as the site.** It survives any one
  machine being replaced, on storage that outlives a machine, and it is not
  copied offsite. A teardown of the site takes it, knowingly. What must
  never be taken is the only good copy of something that should outlive
  what is being destroyed, and the safety officer refuses a teardown that
  would (`scripts/safety-officer`, built). The storage is the hypervisor's
  storage driver for Kubernetes: its token confined to the site's worker
  pool and a storage of its own, the driver installed, and a claim on its
  class attached to a worker and removed again by a nightly test (all
  proven on site0). Prometheus's volume asks for that class. A site whose
  Prometheus already had a claim keeps the old one until it is removed by
  hand, once, and the integration tier says which a site is on.

The site keeps itself, and proves it:

- **12. Every necessary function has a deadline and an alert on missing it**:
  the state backup, each database's backup, each workload's backup, the
  nightly checks, a converge.
- **13. A converge in flight is not evicted.**
- **14. Work is spread again after a roll** (#628), by the descheduler.
- **15. Alerts on the bet read pressure**, not memory in use, and one says how
  long until a resource is full at the rate it is filling.
- **16. How a machine sheds load is declared**, not left at the kubelet's
  defaults.
- **17. The bet has been tested.** Staging is overfilled on purpose and the
  right things are shed in the right order.
- **18. The site says what it is shortest of, and what more hardware would
  buy**: so many more weeks of history for this much disk, this much more
  work for this much memory, none of a capability it has no hardware for.

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

`scripts/contractor/internal/survey` is the first increment of this. It runs on
a peer, probes every peer it can see rather than trusting their status, and
reports one row of the matrix; run on each hypervisor, the rows assemble into
the whole. This epoch owns making it periodic, making its output land somewhere
durable, and deciding what a hole in the mesh should page for.

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

### Alerts go to Slack, and the acceptance test is the Watchdog (agreed 2026-09-17)

**Slack, through the receiver Alertmanager already ships.** No bespoke relay,
no GitHub identity inside the estate, nothing to write. The webhook URL is the
whole credential - an incoming webhook authenticates by being known - so it
arrives from the vault as `alerting.webhook_url`, lands in a Secret created by
`management/cluster/monitoring.tf`, and Alertmanager reads it from a file
rather than from its own configuration, which the operator renders into a
secret of its own.

`alerting` is fleet-level, like `workloads`, because one person reads the
alerts and a second site would report into the same place. It declares
`provider` so a reviewer sees the vendor in git, and carries no
`vault_provider` attestation: that check exists to stop one vendor's
credentials reaching another vendor's API, and the worst case here is messages
arriving in the wrong chat.

**What was designed and thrown away, because it is the useful part of this
record.** The operator asked for an issue to be opened when somebody logs into
the game server, as an acceptance test that alerting works end to end. That
produced a design with a log-reading exporter, a new program to turn alerts
into issues, a GitHub App credential inside the cluster and a container image
to publish - at which point the operator's objection landed: _"we aren't the
first people to ever setup a Prometheus / Grafana capabilities. What is
standard / enterprise?"_

The standard answers were already present and unused:

- **Alert delivery** is Alertmanager's own receivers - Slack, email, PagerDuty,
  Discord, Telegram and the rest are native. A GitHub issue is not a standard
  alert destination, which is why every version of that design required
  writing something.
- **Proving the path** is the `Watchdog` alert that kube-prometheus-stack
  already enables: it fires permanently, by design, so that its ABSENCE is the
  signal. The dead-man's-switch pattern, shipped in the chart this epoch
  already deployed.
- **Alerting on a log line** is Loki's ruler, not a bespoke exporter. Written
  up as #436 and deferred: the benefit is retrospective, the trigger it was
  deferred behind has not fired, and the reason it was asked for has
  evaporated now that Watchdog is doing the job.

So the acceptance criterion is not "an issue appears when a player joins". It
is **an alert reaches a person, proven by the Watchdog heartbeat arriving and
by a deliberate drill** - which is the property the player login was standing
in for.

**Watchdog is routed to Slack every twelve hours rather than to nowhere.**
Routing it nowhere would prove nothing; routing it at the normal interval
would be noise. Twice a day is a heartbeat whose absence a person can notice -
and _noticing an absence is a human job, which is this arrangement's weakness
rather than a claim about it._ The stronger answer is a dead-man's-snitch
service that reports when pings stop.

**Security patrol cannot be that snitch, and the reason is worth recording.**
It runs on a GitHub-hosted runner deliberately outside the estate, and it holds
no credential that reaches in - putting a tailnet key at GitHub is the trade it
explicitly refuses. So it can see that the estate's GitHub-visible liveness has
stopped, which is the failure it was built for, and it cannot see Alertmanager.
Making it the snitch would need the estate to write a heartbeat somewhere
patrol can read - an object in R2 - plus a read credential stored at GitHub.
That is a real option and it was declined for now, not overlooked.

**Grafana has no password and no basic auth.** The chart ships an admin secret
with a password everybody knows, and disabling the login form leaves the HTTP
API accepting it - a door with the sign taken down. Both are off, so there is
no account to guess at and reaching the service is the only gate. That gate is
`kubectl port-forward` today, and replacing it is the first duty of whatever
exposes Grafana.

### The tunnel's first consumer is the game server, reached through WARP (agreed 2026-09-18)

The tunnel was meant to arrive with Grafana. It arrived first for the game
server instead, because the operator could not play from off the LAN, and the
alternative on the table was a router port forward.

**Why not a port forward.** Remote joins fail on this server whenever it cannot
form a direct path to the player (#446). This is a known, unresolved defect in
Valheim's dedicated-server crossplay, reproduced in public reports line for
line, where the only known fix is crossplay off with UDP 2456-2457 forwarded.
Doing that on a shared worker would make the game server internet-inbound on
the same machines as the self-hosted runner, whose token reaches the whole
vault. The workload record judged that path "real but narrow" partly _because_
nothing on those workers was internet-inbound. So a forward means moving it
into a dedicated zone first.

**Why the tunnel instead.** `cloudflared` dials out, and the router gets no
open port. Only a device enrolled by somebody on the member list is given a
route, so the server's exposure stays low and it stays on the shared workers.
It is the model the operator stated when the tunnel was first chosen: "If
you're not on the list you ain't getting in." The laptop joins Cloudflare,
never the tailnet.

**The shape.**

- **Private routing only.** No public hostname. WARP runs in **include** mode,
  so an enrolled device sends exactly the declared routes through Cloudflare
  and nothing else. Include mode is also the only way the routes work at all:
  the default exclude list covers every private range.
- **Routes are fixed cluster addresses.** Each one is a Service with a hand-set
  `clusterIP` from the bottom of the service range, which Kubernetes reserves
  for addresses set by hand. `tests/go/repo/tunnel_routes_test.go` holds each
  route and its Service together.
- **The tunnel's API token is its own, not the bucket admin token,** and is
  held to the three-way vendor attestation. It edits who may enroll and what
  they reach.
- **The tunnel's password is generated** by the contractor into the vault. It
  is written to state, which by the rule in `phases/secrets.go` makes it ours.
- **The member list is personal data, so it lives in the vault.**

**The enrollment application is adopted, never created.** The first converge
stopped on `application_already_exists`: Cloudflare creates the `warp`-type
Access application along with every Zero Trust organisation and allows only
one. `tunnel.tf` now finds it by the name Cloudflare gives it and imports it
(#453). The pull request had named this as a risk. A risk named in a PR body
is still a risk: it should have been designed out before the first apply.

**The pod MTU had to be pinned before the tunnel could carry anything but
TCP.** Since v1.17 Cilium gives every pod the lowest link MTU on its node, and
these nodes carry `tailscale0` at 1280 - an interface pods never leave by,
since pod traffic goes out eth0 at 1450. Every pod was therefore running 170
bytes short, and cloudflared could not complete a QUIC handshake at all: 1280
minus the tunnel overhead is 1230, below quic-go's 1252-byte initial packet. It
fell back to HTTP/2, which carries no UDP, so the game server could never have
been reached through the tunnel however the rest was configured (#455,
cilium/cilium#37529, open upstream). `MTU: 1450` is pinned in the Cilium values
and the cost is stated there: pod traffic that really does route over the
tailnet may fragment, which nothing does inside one site.

**The lesson is about what "connected" proves.** Every surface said the tunnel
was healthy - pods Running, four connections registered, Cloudflare's dashboard
green - while the thing it was built to carry could not pass. The guard is in
the tier that can see it: `tests/go/integration` reads the connector's own QUIC
metrics and fails when they are zero, and a repo guard refuses an unpinned MTU.

**What is not known yet.** Whether crossplay holds a player who arrives through
the tunnel. The tunnel carries connections the player starts, and crossplay
insists on one the _server_ starts. The first test is crossplay on, joining with
WARP connected. If that still fails at +5 s, the second test is crossplay off,
joining by the game server's tunnel address on port 2456. That drops console
players, and puts the friend who plays today on the member list too.

### The control plane is scraped, and it took two tiers (done 2026-09-20)

The stack shipped watching every workload and nothing that runs them. Talos
binds the scheduler and the controller-manager to `127.0.0.1` and leaves
etcd's metrics listener off, so the three components that decide where work
goes and hold the cluster's state had no targets at all.

**It is two changes in two tiers, and each half alone is wrong.** The machine
configuration arrives through a converge; the chart values arrive through
Flux. Enabling the chart alone leaves three red targets, which is how a
cluster teaches everybody to ignore red targets. Converging alone opens three
ports nothing reads. `TestScrapingTheControlPlaneChangesBothHalvesTogether`
refuses either on its own, and the ledger proves it.

**etcd is the one that costs something.** The scheduler and the
controller-manager serve metrics over TLS and delegate authorization to the
API server, so exposing them changes who can _reach_ the endpoint and not who
can read it. etcd's metrics listener has no authentication by design - that is
why it is separate from the client port - so binding it hands cluster health
and sizes to anything that can reach 2381 on a control-plane node. It serves
`/metrics` and `/health` only, and the client port holding the estate's data
is untouched. Narrowing it needs Talos ingress firewall rules, which means
enumerating every port the estate depends on and locking everybody out if that
list is wrong, so it is filed (#468) rather than bundled.

**etcd's addresses are node addresses**, which this repository keeps out of
git. OpenTofu writes them into a secret and Flux substitutes them into the
HelmRelease, the same route the database's identifiers already take. JSON
rather than a comma-separated list, because the value lands in a YAML list
position and JSON's array syntax is a valid flow sequence.

**The scrape does not verify the scheduler's certificate** (#469). The
direction that matters still holds - those components refuse a scrape without
a token carrying `get` on `/metrics` - but Prometheus would hand that token to
an impostor on the node network. Recorded rather than left implicit.

**What was proved afterwards** is in `tests/go/integration`: a healthy target
for each of the three. The previous round shipped this stack and asserted
nothing about it, and it then failed to install for two days without anybody
noticing (#459).

### Full on purpose: the capacity design (agreed 2026-10-03)

Settled with the operator in one conversation, against the first numbers
this epoch's stack produced. Each is a decision; the criteria above are what
they come to.

**The first numbers.** site0, three control planes and three workers, memory
in GiB, over the 1.76 days of history Prometheus held:

| Machine         | Allocatable | Requested | Limits | Peak used |
| --------------- | ----------- | --------- | ------ | --------- |
| control plane 1 | 3.21        | 1.09      | 0.50   | 2.18      |
| control plane 2 | 3.21        | 0.84      | none   | 1.70      |
| control plane 3 | 3.21        | 0.98      | 0.33   | 1.85      |
| worker 1        | 9.22        | 8.78      | 10.25  | 3.36      |
| worker 2        | 9.22        | 1.28      | 1.75   | 1.83      |
| worker 3        | 9.22        | 0.47      | 1.50   | 1.94      |

The machines hold 42 GiB of the host and the work in them peaked at 13.
Control planes use about a gibibyte more than they reserve, so nothing about
them can be judged from requests. One worker is 95% reserved and two are
nearly empty (#628). etcd's WAL fsync, 99th percentile, is in the 32 to 64 ms
bucket on every member against a guidance of 10 (#627). And the history was
1.76 days old because the site had been rebuilt: Prometheus's disk goes with
its machine.

**Overfill in Kubernetes, never at the hypervisor.** Kubernetes knows
priorities and, short of memory, evicts the least important pod. The
hypervisor knows machines, and short of memory it swaps or kills one, which
may be a control plane. So the strict layer is below and the flexible one
above. CPU is the exception that needs no rule: it slows and does not fail.

**Machines are sized by the host; pods are sized by measurement.** With the
hypervisor strict, a machine's size is not a question about use. It is the
host's real memory, less what is taken off it, divided. It changes when the
hardware or the count does.

**The count is computed too.** Holding everything with one worker gone means
two workers are never more than half full, and each further worker raises the
share that can be used until its own overhead, about 0.8 GiB, costs more than
it frees. For some 30 GiB of worker memory that is five or six machines. An
earlier reading of these numbers, that three half-empty workers argue for two,
was wrong for this reason.
**Rejected for now:** adding machines under load (Cluster Autoscaler,
Karpenter). On one host a new machine is the same memory divided again. It
means something when there is a second host, which is
[`10-second-node.md`](10-second-node.md)'s. And scaling replicas with load:
almost nothing here has a second replica to add.

**Tight is not seized.** Two reserves keep the site able to change itself.
One worker's worth is kept inside Kubernetes by the rule above, so a worker
can be drained. One control plane's worth is kept unallocated on the host,
because a control plane is added before one is removed.

**Batch is necessary, and only its timing is not.** The runner is `batch`, so
a converge is, and the state backup, and the nightly checks. Priority orders
who is served and promises the last in line nothing, so at a standing 110%
batch could wait for ever and the site could not then be changed to fix it.
Hence the floor in criterion 6 and the deadlines in 12. The floor is not idle
memory: other pods may burst into it, and a pod over its reservation is
evicted before one within it.

**The computer sizes the computer.** The operator's words: "Computers dictate
how much a computer needs. That's a dumb decision that needs no human
intervention." What keeps it from a mistake is rules, not review.
**Rejected:** a pull request for each resize.

**The first measured baseline, read out before the history it came from was
dropped.** Moving Prometheus onto a volume that outlives a machine loses the
history on the old one, once. On 2026-10-07 it held 5.6 days, and this is
what it said, by namespace, over that whole window:

| Namespace     | Memory, peak | Memory, average | CPU, peak  | CPU, average |
| ------------- | ------------ | --------------- | ---------- | ------------ |
| `kube-system` | 3.98 GiB     | 3.64 GiB        | 0.30 cores | 0.21 cores   |
| `valheim`     | 2.00 GiB     | 1.82 GiB        | 0.23 cores | 0.08 cores   |
| `monitoring`  | 1.24 GiB     | 1.07 GiB        | 0.07 cores | 0.05 cores   |
| `database`    | 0.31 GiB     | 0.18 GiB        | 0.08 cores | 0.01 cores   |
| `arc-runners` | 0.30 GiB     | 0.19 GiB        | 2.00 cores | under 0.01   |
| `flux-system` | 0.29 GiB     | 0.18 GiB        | 0.02 cores | under 0.01   |

Everything else stayed under 0.1 GiB and 0.01 cores.

- **These are a site at rest, and nothing may be sized down from them.**
  Nobody logged in to the game server in those 5.6 days. Its 2 GiB and 0.23
  cores against the 8 GiB and 4 cores it reserves are what an empty server
  uses, and say nothing about one with players on it. The operator, on
  being shown the gap: "It needs to actually experience load to get a good
  measure and that's true for any machine - I don't want us to get into the
  habit of measuring once and then starving our machines when they need it
  the most." The publisher's figure stands, and criterion 9 now says why.
- **History grows at about 0.4 GiB a day:** 2.32 GiB for 5.6 days, so
  fifteen days is about 6 GiB. Above the 2-5 GiB the volume was sized from,
  and a third of the 18 GiB at which Prometheus starts dropping its oldest.
- **One container restarted once** in the window, the database operator.
- **Two alerts fired besides the one that always does:** the operator
  reporting a resource it rejected, for about a day, and the inhibitor for
  informational alerts, for about eleven hours.

Memory in use by machine over the same window, as each machine's own
exporter reports it:

| Machine         | Peak | Average |
| --------------- | ---- | ------- |
| the hypervisor  | 81%  | 80%     |
| control plane 0 | 59%  | 55%     |
| control plane 2 | 49%  | 46%     |
| control plane 1 | 45%  | 42%     |
| worker 0        | 35%  | 33%     |
| worker 2        | 21%  | 18%     |
| worker 1        | 20%  | 19%     |

- **The machines are a long way from full and the host under them is not.**
  No worker passed 35% and no control plane 60%, while the hypervisor sat at
  80% without moving. What the host holds is what the machines were given,
  not what they use - and, on this host, the filesystem's cache, which this
  reading counts as used though it is given back on demand (criterion 2
  takes it off separately for that reason). So the room this epoch is after
  is inside the machines: in what is reserved against what is used
  (criterion 6), and in sizes that are derived and not given (criterion 3).
  The hypervisor's figures are from one day; it was not measured before.
- **The resource the operator rejected was a scrape configuration,** and the
  estate declares one: the hypervisor's. It was rejected for about a day,
  which is the length of time between its manifest reaching the cluster
  from `main` and the site running the release that writes the Secret it
  reads (#618). Nothing is rejected now.

**The order of what is left, agreed 2026-10-07.** First what reads and
refuses and changes nothing that is running: the hardware's facts (2, 4;
the facts are read and carried by the record, and nothing uses them yet),
the budget and its refusal with sizes moved from the code to the config
(5), what each application expects to need (10), the capacity check on a
pull request (6), dashboards (15), and the alerts and deadlines (8, 12).
Then, with [05](05-node-lifecycle.md), what moves a running machine:
derived sizes and counts applied one machine at a time (3), reservations
following use (9), the descheduler and the overfill test (14).

**The facts come from the host, by way of the record.** A site's
hypervisors are asked what they have - memory, processors, display
devices, the size of each datastore, and every machine already on them -
by the contractor, over the API it already verifies, before any run in
the cluster root. The root outputs them unchanged so the state holds them
and the as-built record carries them, which is how a pull request's plan,
able to ask nothing, has them. **Rejected:** a data source in OpenTofu,
which a plan with no credential cannot read and which is read again on
every plan and kept nowhere. **Rejected for now:** Karpenter's provider for
this hypervisor, which sizes machines by best fit as this intends to - it
is alpha, joins machines by cloud-init, holds a token in the cluster that
creates and destroys machines, and makes them outside OpenTofu, where the
address plan, a pull request's plan and the safety officer do not see
them. **Not read from the host's API,** because it is not there: how fast
each network interface is, and the filesystem cache's own figures. Both
are reported by the host's exporter and belong to the dashboards.

**110% was an attitude and not a rule.** The operator, when the capacity
check was proposed with it as a limit: "110% was a number I made up. It
shows you the approach I want to take in the estate - not an actual rule.
We are LEAN and we can FLEX into our capacity... If we can do work then
lets do work and we shouldn't be held back by batch or work that can be
deferred." So only work that cannot wait has to fit, nothing caps the
total, and what limits a site is what its machines can actually do, which
is watched. And when to lend is not a time on a clock: "4 am is also a
random number - patterns is a known number." Capacity is lent by what each
workload is seen not to use, whatever the workload is.

**Lent the way Kubernetes lends it, for now.** Work that can wait reserves
little, runs in what is free and is stopped when the memory is wanted
back; a stopped job says why and is retried, which the operator made a
condition. **Looked at and put off** (2026-10-08): Koordinator, which
accounts for what is safe to lend from recent use and from a longer
prediction, at the cost of several controllers and an agent on every
machine with the run of the host, untried on Talos; and Crane, built
around finding the cycles in a workload's use and unmaintained since 2024.
**Because** the site has almost no work that can wait, and a pattern is
only known once it has been watched. **What reopens it** is in code and
not here: an alert when such work is stopped more than occasionally
(`LendingIdleCapacityNeedsAccounting`), and a guard that fails on the
change that gives a site a second hypervisor. **Built:** each namespace's
reservation recorded beside its use, which is the pattern, and an alert
when must-run work would not fit with a worker gone. The runners are two
sets, split by the priority of their work: converges on one, which is
`interactive` and not to be stopped, and work that can wait on the other,
which is `batch`, reserves little and has a ceiling. A stopped batch pod
raises `WorkThatCanWaitWasStoppedForResources`, which says it was on
purpose and is being run again. **Not built:** a CI job run again when its
runner was stopped, which GitHub does not do unaided. The operator, on why
most of a retry is wrong: "If it failed the first time we shouldn't care
about running it a 2nd time." A job that ran and failed has given its
answer; only one that was stopped has not, and running that one again
needs a workflow started by another to hold write access to Actions, which
`TestAWorkflowStartedByAnotherTakesNothingFromItButItsOutcome` refuses.
The only CI work that can wait is a nightly, whose next run is the retry.
**Build it when** `LendingIdleCapacityNeedsAccounting` fires for CI work,
or the first CI job that can wait and is not on a schedule is declared -
and then only for a job that was stopped.

**Priority work waits, and only for moments.** Offered the choice of letting
work that cannot wait stop work that can, the operator chose otherwise:
"Interactive work should wait BUT we need to make it so that it ONLY needs
to wait for seconds. The batch work can have 4 different hour long jobs BUT
only the batch runner will tackle them - the batch jobs that last only
seconds are able to flex into the priority runner." So nothing is stopped
to make room on the runners, and the protection is in what is let in: a job
says which of three kinds it is - cannot wait, short, or can wait - and how
long it ordinarily takes. Long work that can wait may ask only for its own
runners. Short work may also fill the priority runners' idle room, and is
short by a time limit of two minutes or less that GitHub enforces. And a
declaration is checked against what happened: "a guard looks at the actual
against the declared and confirms that they align" - the middle of a job's
recent runs must be no longer than it says and no less than a third of it.
**Honest limits:** GitHub's smallest time limit is a minute and a runner
takes tens of seconds to start, so moments means a minute or two; jobs are
handed to a set in the order they arrive; and no short work that can wait
runs on these runners yet.

**Where a size came from is data, not a comment.** The operator, on the
proposal that a manifest's reservation is the declaration and only its
source needs adding: "comments are lost. If it's not in code then it is
next to worthless." So each application and the core say, container by
container, which of three each reservation is, and a measurement has to
name how long the thing was watched and what it was doing - an idle
reading cannot be entered as one. Nearly everything starts as an estimate,
the game server's memory as the publisher's figure, and nothing as
measured. **Rejected:** a second statement of the needs themselves in the
application's declaration, which would be the reservation written twice.

**The hypervisor's budget, and each thing taken off it.** Before any run
in the cluster root, what each hypervisor has is held against what the site
asks of it, and a site that asks more is refused with the whole sum
(`scripts/contractor/internal/budget`). From the host's memory come off:
two gibibytes the hypervisor keeps for itself; its filesystem's cache, at a
tenth of the host and never more than sixteen gibibytes, which is the
hypervisor's own default; whatever is given to machines on it that are not
the site's, templates aside; and one control plane's worth, so a machine
can be replaced by a new one before the old one is gone. Every figure is
printed with the answer, so a refusal can be argued with. **Not yet:** the
cache is not held to that figure on the host, so that deduction is a
default and not a fact until the playbook sets it; processors are not
budgeted; a pull request's plan does not refuse, because it has no
rendered config to count machines from, and takes this up with the
capacity check (6); and the sizes are still the cluster module's, with a
guard holding the budget's figures to them (#615).

**Tight does not mean seized.** A site is refused a hypervisor only when it
asks for more there than it already has and that does not fit. One that is
over and asking for what it has, or for less, is told so on every run and
not stopped: the machines are there either way, and the converge that
would shrink them is the one a flat refusal blocks. site0 fitted its first
budget with a tenth of a gibibyte to spare (62.3 on the host, 42.1 for the
site, 42.0 asked), so a host reporting a little less memory after an
upgrade would otherwise have stopped every converge. A machine the plan no
longer asks for is still the site's while it stands, by its id being in
the site's band.

**A host says what it has only when the site owns it.** Asked on rented
hardware the same reading says something else each time, and the operator
asked which: "would the data center say 'sure you can have 10,000,000
cores' or would it say 'your contract says 4 cores so here is 4 cores'?"
The second, and it is still not the whole answer:

- **A dedicated server** is read as the machine it is, like one at home.
- **A virtual server** is read as what the plan gives it, and those
  processors are shared. The count is right and the capacity behind it is
  not guaranteed; what the provider took back shows as steal time, which
  the host's exporter reports and the reading cannot. It belongs on the
  dashboard beside the count.
- **Capacity that is asked for on demand** has no host to read. What could
  be had is unbounded and every unit of it is billed, so a site of that
  kind takes its capacity from a ceiling written in its config - what was
  agreed to be paid for - and the budget refuses anything past it.

So a site's capacity has two sources, read from a host it owns or declared
as a ceiling it rents under, and the budget (5) is where they meet. Only
the first is built, and only site0 needs it.

**Worth keeping for as long as what it describes is alive.** The ruling that
replaced the one below, on 2026-10-06, when the first design for criterion 11
was costed against the storage vendor's free allowance. The operator's words:
rented storage "really should be there for CORE storage. It's what MUST be
offsite and used for node / site / estate recovery", and "an estate's metrics
is valuable as long as the estate is alive. A site's metrics is valuable as
long as the site is alive. A node's... a machine's". So every asset declares
the scope whose lifetime it has (machine, node, site, estate, or client for
what belongs to somebody else), the scope its working copy dies with, and
where another copy is kept and how old it may be
(`scripts/details/holds`). Each scope keeps its own detail and passes a
summary up; the estate's tier waits for a second site.

**A teardown stays total, and does not start when it would lose something.**
"I still stand by demolish is TNT but we should really never use it when
there is value." **Chose** a safety officer: a program of its own that is
asked before a site is destroyed, reads what the site holds, looks at each
copy itself with a key that can read a bucket and change nothing, and
refuses by name. **Chose** no way past it - no flag, no prompt: make the
copy, or stop the site holding the thing. **Rejected:** an override typed at
the terminal, which is one more thing typed from habit; and a line in the
config that gives an asset up, which is an order by another route.
**Because** the contractor holds the detonator, and the party that says the
explosion is safe cannot be a step of it. What enterprise calls deletion
protection and a recovery point objective are the same two ideas; the
declared age a copy may be is the second, and it is what makes the officer
measure a fact where it would otherwise read a claim.

**The game world stays in rented storage, as an exception.** It is a client's
data. It moves when the cluster is stable.

**A pull request's plan is our own, and the tools that usually do it were
looked at after the fact.** The plan a pull request gets
(`contractor plan-as-built`) was built without the comparison being written
down, which the operator asked about on 2026-10-06 when a change to a module
planned as "no changes" and its effect only showed on the later change that
moved the site's pin. The standard tools are Atlantis, a server that plans
and applies from pull request comments, and Digger (renamed OpenTaco in
November 2025), the same idea run inside the repository's own CI with a
small orchestrator beside it; Terraform Cloud, Spacelift and env0 are the
hosted form.

**Kept** our own, for what they all assume. They plan a pull request against
the real state with the real credentials, and post the plan as a comment.
This repository is public: the plan would publish hostnames and addresses
the config keeps in a vault, and the credentials would have to be within
reach of a pull request, where today a pull request's plan holds none and
runs against a recorded copy with stand-ins (#554). The state is also a
database inside the cluster, encrypted with a key from the vault, which a
hosted runner cannot reach and the estate's own runner refuses to serve a
pull request from. And each needs something that listens: Atlantis a server
GitHub can reach inside the estate, Digger its orchestrator, hosted by its
vendor or by us.

**What they have that this does not,** and where each is answered: applying
after a merge (the converge), noticing drift (the nightly plan against real
state), locks between concurrent pull requests (one operator), and policy
on a plan (the repository's guards). **What was missing** was a preview of
a module change against the sites that would take it, which every one of
them gives; it is now the second half of the plan comment. **Not verified:**
whether Digger can redact a plan or plan a fork's pull request without
credentials; nothing in its documentation says it can. **Would change
this:** the repository going private with its state somewhere a runner can
reach, at which point Digger is the first thing to try before maintaining
our own.

**Not built:** the officer is asked about a site's teardown only. A machine's
retirement ([05](05-node-lifecycle.md)) is where a site's history and its
state are endangered, and it is asked there when that work resumes. The
estate's own teardown is not asked.

**History is production data.** Superseded by the two above. The operator's
ruling when the 1.76 days was found. **Chose** a snapshot to the bucket and a
restore on start, the way a workload's data already goes (#588). **Rejected:**
Thanos, the standard store for Prometheus on object storage and the first
choice made, given up for three or four more programs running on a site being
packed tight; and a fifth bucket, when the production bucket is already the
one a teardown keeps.

**An application's needs are declared before they are known.** Nothing can
be measured before it runs, and the plan has to count it before it is merged.
So an application states what it expects, from its publisher's own figures
where there are any, and says that is where the number came from. It runs in
staging, the measurement replaces the estimate, and the autoscaler keeps it
true after that.

**Left out, knowingly.** Replicas as protection against losing hardware:
every machine shares the one host, so a replica protects against a roll and
nothing else, and what protects the estate is the backup and the rebuild. A
restore drill on a schedule is the habit worth taking from that. Service
level objectives and error budgets, which the deadlines stand in for.

### Two more roles: the surveyor and the engineer (agreed 2026-10-03)

The contractor is some 11,000 lines against the clerk's 2,200 and the
lawyer's 700, and the question was whether that is one role. Size is not the
test this repository uses; what a program holds and can break is. By that
test building, converging, demolishing, restoring and retiring are one role:
each holds the site's token, its state and its machines' admin, and two
programs with the same power would isolate nothing. Generating the site's
own secrets, the exporter's certificates among them, uses that token and
stays.

**The seam is the work that holds no credential.** `plan-as-built` needs no
vault and no state, and `survey` only probes; both ship in the binary that
can demolish a site. This epoch adds a body of work of the same kind - the
hardware's facts, the capacity check, what the site is shortest of - and it
is built outside the contractor from the start.

**Chose:** two roles, neither holding a key.

- **The surveyor measures.** What the host has, what answers on the
  network, what is used and at what rate. It reports and decides nothing.
  `survey` moves to it, and #239 and #154 are its.
- **The engineer says what the site can carry.** It takes the surveyor's
  facts and the ratings written in this record, and stamps or refuses a
  change before it is built: the capacity check, and `plan-as-built`, which
  moves to it. The ratings are the operator's. The engineer applies them and
  has no authority to set one.

**Not a role:** whatever generates a certificate. It is one more secret a
site owns. The case for a signing role is
[`10-second-node.md`](10-second-node.md)'s, where a key is kept and two
signatures have to stay apart.

### The host is measured, and it took three tiers (built 2026-10-03)

Criterion 1. The estate saw its machines and not the host under them, so the
one number a site's capacity comes from was nobody's to read.

**The exporter is Debian's package of the Prometheus project's node
exporter**, installed by the hypervisor playbook without its recommended
extras, which are scheduled collectors run as root that nothing here reads.

**It answers on the host's own address**, the one the cluster already reaches
the Proxmox API at. The first design bound it to the site's gateway, to keep
it off the network the hypervisor sits on, and that could never have worked:
a zone is a VRF, a routing domain of its own, and the gateway's address is
inside it. A program in the host's ordinary domain can neither answer on
that address nor reach it. The playbook's own check waited on a connection
that never opened, and it took three runs on the estate to find out why,
because the check reported only that it had failed. It now reads what the
host says about the exporter and reports that. On the host's own address the
exporter is reachable from that network, and what protects it is the
certificate it asks for.

**It serves TLS and asks its caller for a certificate.** The first version
served plain HTTP and excused the policy scan's objection to it, on the
ground that the request never leaves the host. The operator turned that down:
"We spent a LONG TIME building out the capability so we need to actually use
it instead of building around it." The capability is the contractor
generating what the estate owns end to end and keeping it in the vault. So
the contractor generates an authority, a certificate for the exporter and one
for its scraper, and drops the authority's key when it has signed them; they
are one item in the site's vault, all or nothing, and replacing them is
deleting the item. The playbook installs the exporter's half and the platform
gives Prometheus the scraper's. That also closes what plain HTTP left open:
any pod in the cluster could have read the host.
**Rejected:** the exception. And a password in place of the scraper's
certificate, which needs bcrypt and the contractor takes nothing from outside
the standard library.

**Three halves in three tiers.** The playbook installs the exporter. OpenTofu
writes a Service with no selector and the hypervisors' addresses behind it,
and the Secret the scraper reads, because Flux can make neither: one is an
address this repository keeps out of git and the other a credential. The
Flux tree holds a `ScrapeConfig` that names the Service and the Secret and
nothing else.
`TestMeasuringTheHostChangesAllThreeHalvesTogether` refuses any one alone,
`TestTheHostsExporterHasOneName` holds the four places its name is spelt to
one, and the integration tier asks Prometheus for the host's total memory by
name, because a target that is up proves a scrape and not a measurement.

**Two of the three do not arrive with a merge.** The playbook is run by the
hypervisor phase, which a converge leaves out, and the Service and the Secret
are written by a module the site runs at its pinned release (#618). The
scrape is written so that this is quiet: it carries no substituted value, and
the operator leaves out a scrape whose Secret it cannot read. An earlier
draft substituted the address into the chart's values, which until the pin
moved would have been a value that was not there.

**They are kept base64-encoded on one line, and the first version did not.**
It stored PEM blocks. The config is a JSON template and a vault's value is
substituted into it as it is, so the line breaks in a PEM block made the
rendered config unparsable: the converge for the merge failed at Render,
and so did every other verb that renders, for as long as it stood. The
runner's key had been kept base64-encoded for exactly this reason since
epoch 01 and the precedent was not followed. A test now substitutes each
generated value into JSON the way the render does, and a set stored as PEM
blocks is refused by name before the render can meet it.

**The certificates last ten years and nothing renews them.** The scrape
failing is what will say so.

**Each hypervisor is measured at its own address**, so a site with two is
measured as two. Bound to the gateway, which every host of a site shares,
it would have been one at a time.

### Open: the account holds two user API tokens and the tunnel uses one of them

Parked deliberately on 2026-09-21 rather than resolved. The operator's position
is that the user-scoped Cloudflare API tokens are not to be touched while the
tunnel work is in flight; this is the note so the question comes back with it
rather than being rediscovered.

**What was found.** The Cloudflare account carries two user API tokens that
could plausibly be `tunnel.api_token`, and nothing in the repository says which:

| Token                         | Permissions                                                    | Resources         |
| ----------------------------- | -------------------------------------------------------------- | ----------------- |
| "Cloudflare Tunnel API Token" | Cloudflare One Networks, Cloudflare One Connector: cloudflared | 1 account, 1 zone |
| "Homelab"                     | Cloudflare Tunnel, Zero Trust                                  | all accounts      |

**Two things are established and worth not re-deriving.**

Nothing in this repository uses a **zone**. Every Cloudflare resource in
`management/cluster/` is scoped by `account_id`, and there is no `zone_id`,
`cloudflare_zone` or DNS resource anywhere in the tree. So a token carrying a
zone grant is carrying something no code asks for.

The six resources on `provider = cloudflare.tunnel` need, between them: Account
· Cloudflare Tunnel, Account · Cloudflare One Networks, Account · Zero Trust
(for `cloudflare_zero_trust_split_tunnel`, which is a device setting), and
Account · Access: Apps and Policies (for the enrollment application and its
policy). The first token in the table does not cover the last two, which
suggests it predates the enrollment app and was superseded rather than
replaced - but that is inference from permissions, not evidence.

**How to settle it without matching dashboard rows**, which is where this
stalled: do not identify the live token, replace it. Create one token with
exactly the four permissions above and no zone, put it in the vault, converge,
confirm a device still enrolls and reaches the game server, and only then
delete both of the old ones. Whichever was live goes with the one that was not,
and nobody has to prove which was which. Deleting before the converge would
remove the working credential, so the order matters.

**Why it is worth coming back to.** An account-wide token with Zero Trust edit
can change who may enroll a device and which private routes an enrolled device
is given. That is the reach the tunnel design is built on being narrow, so a
second one nobody can account for is a gap in exactly the control this epoch's
tunnel decision rests on.

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
