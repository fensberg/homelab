# Epoch 05 — Node Lifecycle

- **Tier / path:** `management/`, `clusters/management/`
- **Branch:** `epoch/05-node-lifecycle`
- **PR:** #
- **Status:** In progress - the machine roll, 2026-10-15

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

## Acceptance criteria moved here from epoch 04

Moved on 2026-10-08 from [04](04-observability.md), which agreed them on
2026-10-03 as part of running a site full on purpose. Each one changes a
machine or a workload that is running, which is this epoch's to do; that
one only measures and refuses. They keep the numbers they had there, so
that what refers to them by number still finds them. Where one says "the
capacity above" or names another criterion, it means epoch 04's.

- **3. No machine's size or count is a literal** (#615, #566). Both are derived
  from the capacity above, the overhead each machine carries, and the
  largest single thing that must fit on one - and then rounded down to a
  step on a short ladder of standard sizes, whole processors and round
  amounts of memory, so a machine is "6 and 12" and never a fraction. The
  count of workers is derived once applications say what they need (10).
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
- **14. Work is spread again after a roll** (#628), by the descheduler.
- **16. How a machine sheds load is declared**, not left at the kubelet's
  defaults. Moved here on 2026-10-15: declaring it changes every running
  machine, and the plan for it applied the change to all of them at once,
  in no order (#767, parked until this epoch can roll it). What it was
  sized from is kept so it is not measured twice. On site0, the week to
  2026-10-15, each machine's memory in use outside pods at its 99th
  percentile: control planes 957-1048 MiB, workers 934-1049 MiB, the
  untrusted worker 885 MiB. The kubelet's defaults hold back 512 MiB on a
  control plane and 384 MiB on any other, so pods are promised memory the
  machines already use. #767 holds back 1088 MiB, 1088 MiB and 896 MiB,
  with work put off gently below 256 MiB free and at once below 100 MiB.
  It takes about 700 MiB from what each worker can offer pods, which the
  game server's 8 GiB no longer fits beside; #767 lowers it to 6 GiB, the
  publisher's lower figure for an explored world, by the operator's call,
  and that release has to land before the machines change.
- **17. The bet has been tested.** Staging is overfilled on purpose and the
  right things are shed in the right order.

## Decided 2026-10-15: rolled by Ansible, shaped like the standard

**Chose:** machines are changed one at a time by an Ansible play run with
`op run -- ansible-playbook`, one host at a time (`serial: 1`), each step a
standard tool: `kubernetes.core.k8s_drain`, which honours disruption
budgets; `talosctl upgrade` or `talosctl reset --graceful`; a health wait
before the next. A machine that will not come back is replaced, not
repaired. OpenTofu declares what the machines should be and never orders
them; Flux keeps what runs on them safe to move.
**Because:** the operator: "I like standard. It's a transferable skill."
The ordering is what Ansible's rolling plays are for, and building it into
the contractor would have been one more piece of the contractor re-doing a
standard tool's job (epoch 06 now replaces those, one criterion each).
**Rejected:** #626's `retire` phase in the contractor, closed; its design is
the play's - the order of operations, and the refusals before anything is
touched: etcd not voting, a database of one instance, a volume nothing will
move, the runner's own machine. Cluster API and Omni, for now: the Talos
Cluster API providers are in community hands with Sidero's development
ended, and Omni holds every machine's admin credential for good, owns the
Talos configuration, sits above every site, and needs a commercial licence
for anything a business depends on. Omni can import an existing Talos
cluster, so neither is closed off.
**The credential:** the play's Talos and cluster credentials come from the
vault through `op run` and live as long as the run, as the converge's do.

### Built 2026-10-15: the machine roll

`management/roll/talos-upgrade.yml`, run by the deploy workflow's `roll` job
every hour, under the converge's lock; the play rolls only between 02:00
and 06:00 on the site's own clock, its `timezone` in the config template.

- **What it rolls to.** `modules/infrastructure/machine-image` is the one
  place a site's Talos and Kubernetes versions and schematics are declared;
  the cluster root builds machines from it, and the platform root writes it
  into `kube-system/machine-image`, which the play reads. A node whose
  `osImage` differs is behind.
- **Its access.** The converge keeps the Talos client configuration in the
  site's vault (`op://<site>/talos/talosconfig`, written when it changes);
  the job hands it to the play through `op run`, and the play has Talos issue
  a cluster credential for the run. Both live in a directory the job removes.
- **What it refuses, touching nothing:** outside the window; a node not
  Ready; an etcd member not voting or missing; a database under two
  instances or not all ready; a version Talos does not move between (a minor
  skipped, crossed without `talos_upgrades_from` saying so, or moved
  backwards); a canary that is not well. A workload whose backup cannot be
  taken stops it as well.
- **The canaries.** The untrusted worker is rebuilt on the new version at
  merge, so it is the first: Ready on the new version, nothing alerted about
  it since it was built, and a pull-request job passed on it since. The first
  trusted worker is the second: fifteen minutes' soak, the site's health held
  three times, and the integration tier run against the site, which must
  pass.
- **Each machine:** an event; its alerts silenced by name and address for 45
  minutes at most; drained through the eviction API; `talosctl upgrade` to the
  installer image; back Ready on the new version; the site's health (Talos,
  etcd, every Flux layer, every database instance, every DaemonSet, no pod
  restarting that was not moved, nothing put off its machine for room);
  uncordoned; etcd defragmented after a control plane; the silence lifted;
  an event. Workers first, then control planes.
- **Its own machine** last: cordoned, and a second job, placed elsewhere,
  finishes it.
- **A failure** stops it with the machine left cordoned. The aftermath opens
  one issue (a refusal that repeats each hour is a comment on it) and, when
  the bump was a patch release and a machine was touched, a draft revert of
  the module; merged, the next window rolls the moved machines back. Across a
  minor there is no revert.
- **After every machine,** the integration tier again, whose plan of the site
  must find nothing to change.

Tested whole in `tests/go/repo/machine_roll_test.go`: the real play against
Kubernetes' own store and API server, with `talosctl`, the clock and the
outside world stood in for, each refusal tripped and nothing touched.

**Owed on site0 before it is trusted**, with a patch release (v1.13.9 to
v1.13.11), the record's own three proofs: a marker on a database-style volume
survives its machine's upgrade; the image's extensions are still there
afterwards; the bump's plan replaces no machine and none of the cluster's
secrets, and the plan after the roll finds nothing to change.

## Not built, from the discussion of 2026-10-15

Agreed or raised while the roll was designed, and left for later initiatives.
Each is a piece of its own, not a debt of this one.

- **The Kubernetes stage.** `talosctl upgrade-k8s`, after a whole Talos roll
  has succeeded, the first control plane its canary, and forward only:
  Kubernetes does not downgrade. Before it, every manifest Flux applies and
  every object in the cluster checked for APIs the new version removes, with
  Pluto, a new supplier that comes in with this stage.
- **Rolling machine configuration** (the second play). OpenTofu writes each
  machine's configuration and the play applies it one machine at a time with
  health between, instead of OpenTofu applying it to all at once. It unparks
  criterion 16 (#767), and changes who applies configuration, which ties into
  epoch 06's Terragrunt work.
- **Retiring machines** (the third), which is this epoch's acceptance test:
  `talosctl reset --graceful` in the same play family, etcd membership
  removed, 5 control planes to 3 by a merge.
- **The dead host.** A machine that will not come back is replaced rather
  than repaired; the replacement path itself is not built.
- **A compliance check after the roll.** `kube-bench`, the CIS benchmark of
  the running site, a new supplier.
- **The game server joined from outside** (epoch 04, criterion 23). When it
  exists it belongs in the soak: it is the one check a player would notice.
- **A "go" before a roll.** Decided against for now: the roll runs inside the
  window with nobody involved, to be revisited if rolls fail too often.
- **A queue that never drops a converge.** GitHub keeps one run waiting per
  lock and cancels an older waiting one, so a roll timed while one converge
  runs and another waits would cancel the waiting converge. The timer fires
  every hour, because each site keeps its own clock and the play decides
  whether its window is open; outside a window the run is over in a minute,
  but it still takes the lock. A queue that holds every converge is not
  built.
- **The roll waits on a pull-request job.** The canary's last check needs a
  job to have passed on the untrusted worker since it was rebuilt, and the
  nightly checks are what usually provide one. A bump merged after them waits
  a night.
- **Cluster API and Omni, again**, when Sidero's Talos providers settle under
  community maintenance, or a second node is bought (epoch 10). The roll is
  shaped like Cluster API's model - declared versions, ordered machine
  replacement, health between - so either could take it over.
- **Cross-host placement**, with a second hypervisor (epoch 10).
- **Runners.** A hot tool - a runner already waiting with everything in it -
  and a waiting runner for every job of a push (the swarm); the lifeline's
  router, generic to any estate, sending only jobs that hold nothing to
  GitHub's runners when the pools fall behind; the heavy lanes moving to the
  pools for good; a third pool runner and the runners' sizes; the converge
  runner's memory, sized from measured use.
- **The game server's own machine**, rather than three workers each sized to
  hold it (three options, not decided), and its volume onto the storage
  driver (epoch 04, criterion 31, not decided).
- **The record keeps refusable fields for more kinds** (#771), and **one
  refused object stops a whole layer** (#769).
- **The workstation's build half** is not run by its test, which stops
  before the cloud image is fetched.
- **Dashboards and alerts** for the roll, deliberately: the events and the
  silences are collection, not a view.
- **The contractor's other pieces** go to standard tools in epoch 06,
  criteria 6-15.

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

### Rejected 2026-10-03: Tuppr

[Tuppr](https://github.com/home-operations/tuppr), from
[ionfury/homelab](https://github.com/ionfury/homelab), is a small controller:
a `TalosUpgrade` and a `KubernetesUpgrade` resource each name a version, a
maintenance window and health checks written as CEL, and it rolls the nodes
one at a time. It was this record's undecided candidate for upgrades in place.

**Rejected** because it holds the Talos admin credential in the cluster for
good, which is the objection upheld against Cluster API's providers. The roll
takes what it had - a window, health between machines, one at a time - and
holds the credential only for the length of a run. The proofs it listed are
the roll's: the data partition survives (a sentinel on a volume), the
schematic is kept (the installer image carries it), and OpenTofu reads the
same version (#339; one module declares it for both).

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

### Adding a node needs no manual step after the vault entry

Agreed 2026-10-02, while closing epoch 02, where this began as "adding a
node needs no commit". Not built. Credentials go in the site's vault and the
site takes up the capacity; nothing else is done by hand. This epoch owns it
because every part of it is about a machine joining, moving or leaving, and
it is built before any site has a second node.

- **The nodes a site has are checked, never declared and never guessed.**
  The contractor asks the site which nodes are there and assigns work
  accordingly. One up is a site with one node; two up is a site with two.
  A node gets a default share of workers.
- **Machines may move when a node is adopted**, so the work is in making a
  move safe - cordon, drain, remove the etcd member, replace, wait for
  health - which is this epoch's own subject.
- **A Proxmox cluster per site**, not nodes standing alone. So the site's
  one API token covers a node the moment it joins, and the site's runner
  mints and stores nothing: its vault access stays read-only and no new
  vault is needed. The costs are Proxmox's own: a join needs root on a
  member that is already running, every member is root on every other, and
  two members are not a quorum when one is down. **Open:** where the third
  vote lives.
- **Push, with a bootstrap key that is soon worthless.** The vault entry
  holds a key the new server accepts only until it expires. The site's
  runner finds the entry, prepares the server with the existing playbook,
  removes the key, and proves it dead by trying it. Only after that refusal
  is the node adopted and given work. Every step fails closed: an expired
  key adopts nothing and is reported; a key that still works leaves the node
  unadopted. The aim is not that the key cannot be stolen but that a stolen
  one is worth nothing.
- **Enrolment is signed, and there are two signatures.** One says the box
  is genuine and which bootstrap key unlocks it; it is the builder's. The
  other admits the box to a cluster; it is the cluster owner's. Each member
  carries a narrow door that admits only an enrolment so signed, so a
  stolen door key, a compromised runner or an altered vault entry admits
  nothing. The expiry lives in the signed record and not on the box, so an
  expired key is renewed by signing a new record into the vault. Today both
  signers are the operator; they are kept apart from the start because the
  estate may one day ship a server to somebody else, and a builder whose
  signature admitted nodes to another's cluster would hold root on it.
- **Security owns the key.** Its checks run on the site's runner, in the
  job that adopts: before the contractor may use a key, that it is signed
  and unexpired; after, that it is dead; every run, that no member still
  carries one. The patrol cannot do this - it holds no credential that
  reaches the estate, by design - and only confirms the job ran.
- **Not the lawyer's.** The lawyer is the estate's. A node is capacity
  inside one site.
- **A server that is shipped has to be worthless in transit**: it carries
  no secret of any site, and its disk unlocks only for an untampered boot.
  FIDO Device Onboard is the standard this resembles; read it before
  designing against it.

**Open:** the third quorum vote; and whether the adoption runs on GitHub's
scheduler, which is late more often than not, or on a timer inside the
site.

## Deferred

- **Machine classes (#566).** VM sizes are literals in `compute.tf`: control
  planes 4 cores / 4 GiB, workers 6 / 10 GiB, disks 64 and 32 GB. They belong
  in named classes a site picks from per role, as cloud instance types are.
  Deferred until a second host with different hardware makes the literals
  wrong; found in epoch 02's abstraction review, 2026-09-27.
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

Since 2026-10-15 a running machine is moved by the roll instead, in place,
from the version and schematic `modules/infrastructure/machine-image`
declares. A new machine is still built from its template.

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
