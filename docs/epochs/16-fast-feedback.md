# Epoch 16 — Fast Feedback (working title)

- **Tier / path:** `.github/workflows/`, `tests/`, `clusters/core/`
- **Branch:** one per piece, from `main`
- **PR:** #
- **Status:** Proposed. Next, with [15](15-expiring-keys.md). 16 starts first,
  because every pull request needs it and 15 will land through it. Epochs 06 and
  08 are on hold, and what these two needed from them has moved here.
- **Closed:**

## Goal

Merged work reaches a site quickly, and we know how quickly. Lead time, from
merge to running on a site, is the headline number. Under it, a pull request
gets its answer in ten minutes, on the shape the industry already uses, and
something owns both numbers. What the pipeline does is split by the question it
answers, instead of every question being asked on every push.

A workflow is thin: a job calls one command, and that command is what a person
runs. That rule is what lets everything else here, and in
[15](15-expiring-keys.md), move without being rewritten.

## Why

On 2026-10-31 a run on #772 took 42.5 minutes: 141 minutes of job time and 92
minutes of queueing. The same day #767 was killed in the merge queue with no
test having failed. Nothing owned the pipeline: lanes were added, limits were
raised when they were reached, and three speed-ups already agreed (10-16 and
10-25) were never built.

This is a solved problem. The answer is the deployment pipeline from
_Continuous Delivery_, and this repository had drifted from it.

## Names

Agreed 2026-10-31. A check reads `kind / subject`, says what runs, and leaves the
tool to the logs. They land in the second pull request, because a required check
cannot be renamed in one step: the one doing it would be held to a name it no
longer reports. So the first adds one gate, `tests passed`, and moves the ruleset
to it. The tiers keep their names: unit, contract, integration, api, e2e.

| Today                        | Becomes                          |
| ---------------------------- | -------------------------------- |
| Format (pre-commit)          | lint / format                    |
| Shell Lint (ShellCheck)      | lint / shell                     |
| Spelling (codespell)         | lint / spelling                  |
| Vet (every tier)             | lint / go vet                    |
| Validate (tofu, kustomize…)  | validate / infrastructure        |
| Test (Go modules, OpenTofu)  | test / unit                      |
| Test (vitest)                | test / frontend                  |
| Repository invariants        | test / repo policy               |
| Application removal          | test / app removal               |
| Meta-tests, part 0           | test / mutation (1 of 4)         |
| Pool Proof (builds)          | test / runner isolation (builds) |
| Egress Proof (the two)       | test / egress allowed or blocked |
| Secrets Scan (TruffleHog)    | scan / secrets                   |
| Logic Scan (Semgrep)         | scan / code                      |
| IaC Scan (Trivy)             | scan / infrastructure            |
| Dockerfile Scan (hadolint)   | scan / containers                |
| Workflow Scan (zizmor)       | scan / workflows                 |
| Policy Scan (Checkov)        | scan / policy                    |
| Already tested (merge queue) | skip if already tested           |

**Moved on 2026-11-01 from the first two pull requests into the third, the bake:**
hook timing and delivery metrics. They need a scheduled job, and the bake adds one.

## How others do it

Read from their repositories on 2026-10-31. The default run on a pull request
is small everywhere, and heavy work is requested, scheduled or follows the
change.

| Project    | On a pull request                                 | Heavy work                                 | Runs on                      |
| ---------- | ------------------------------------------------- | ------------------------------------------ | ---------------------------- |
| Talos      | 11 of 64 jobs, including lint, unit and short e2e | The other 53 by label, or by trigger       | Self-hosted, public repo     |
| Ansible    | `ansible-test --changed`                          | `ci_complete` in a commit message; nightly | Azure Pipelines, one Summary |
| OpenTofu   | A `fileschanged` job decides the lanes            | Nightly                                    | GitHub's runners             |
| Flux       | `make test`                                       | Separate end-to-end workflows              | GitHub's runners             |
| Cilium     | Smoke                                             | A `/test` comment                          | Comment-triggered            |
| Kubernetes | Presubmits                                        | Postsubmit, periodic; Tide batches merges  | Its own cluster              |

Shared: CI calls the command a developer runs. Talos generates its workflow
rather than keeping it by hand. None of the workflows read uses
`harden-runner` (Talos, Flux, OpenTofu, 60 of Cilium's 98). Ansible wrote its
own change detection, so a custom one is not a failure, though path filters come
first.

## Who owns what

So that nothing has two owners and nothing has none.

| Work                                             | Owner                                                                                 |
| ------------------------------------------------ | ------------------------------------------------------------------------------------- |
| The checks and the pipeline's shape              | 16                                                                                    |
| Thin workflows: the logic leaves the YAML        | 16. The language it lands in does not matter here; retiring shell stays 06's, on hold |
| Site promotion (Renovate) and signing releases   | 16, moved from 08                                                                     |
| Removing `harden-runner`, job by job             | 16, as each job's list is replaced                                                    |
| The 12 credentialed jobs on GitHub's runners     | 16, each with its own answer                                                          |
| The jobs that hold a site's keys, and their list | 15                                                                                    |
| Sealed lanes                                     | 16                                                                                    |
| OpenBao                                          | 15                                                                                    |
| Whether a site pulls a signed snapshot           | 15, at its close                                                                      |
| Roll back of the machines                        | 05's plan; 16 measures time to restore                                                |

## What each word means here

**Commit and push do not change.** The hook stays under ten seconds; that rule
stands, and nothing here moves work into it. "Commit stage" is the book's name
for the first checks **CI** runs after a push, while the author does something
else. The ten minutes is that wait, down from 42 minutes.

## The stopgap: piece 0, before anything else

**The problem.** Repository invariants sits on Go's 30-minute limit. The job
was 16 to 24 minutes until #772, then 32.3, 32.1 and 32.3: passed, passed,
killed. Whether a pull request's run finishes is now luck, and it applies to
every pull request.

**The cause.** #772's roll tests added 427 s to the lane. The removal guard runs
the whole suite twice, so they run twice more inside it, taking it from 872 s to
1,353 s. #772 passed at 1,772 s, 28 s under the limit, and its queue entry
reran nothing. #767 was then the first full run at that size, and it took
longer. A failed queue entry for #840 on 10-29 took 31.4 minutes and has not
been examined; it may be the same limit.

**The change.** The removal guard gets a job of its own, named once in
`REMOVAL_TESTS`, which the invariants lane skips. The `Test` gate waits on it,
so no required check changes. It takes the primary pool, which goes to the
longest job, and invariants moves to the builds pool. Its copies inherit the
lane's `-skip`, so the new job passes the same one, or they would run the
coverage guard. It is a workflow change, so it is handed over as a patch.

**Expected, not measured:** invariants at about 9 to 10 minutes, and the removal
guard at about 23 in its own job. The whole run gets little shorter, because
there is one more job for the same runners. This stops the timeouts and no more.

**Rejected:**

- **Skipping the roll tests inside the removal guard.** It was recommended and
  was wrong: `management/roll/talos-upgrade.yml` reads every application's
  `application.json`, so the dependency is real and the guard must see it.
- **Raising the limit.** That is how the lane reached 32 minutes.
- **Requeuing and hoping.** It may pass, and the next pull request has the
  same odds.

## The target

| Stage        | What runs                                              | Target                                      |
| ------------ | ------------------------------------------------------ | ------------------------------------------- |
| Local hooks  | Format, fast guards                                    | Under ten seconds, as now                   |
| Commit stage | Compile, unit tests, static analysis, changed proofs   | Ten minutes                                 |
| Merge queue  | The whole commit stage against the latest `main`       | The same, or nothing if the tree was tested |
| The bake     | Whole ledger, removal, sweeps on each commit to `main` | About half an hour                          |
| Promotion    | The pin moves when the bake passes                     | Unattended                                  |
| Nightly      | What truly needs a schedule                            | Alerts                                      |
| Monitoring   | Runner health, drift, live state                       | Continuous, not CI                          |

Three questions, three schedules. _Is this change right?_ runs on every push.
_Can our checks be trusted?_ runs on `main` after a merge. _Is the running
estate healthy?_ is a monitor. A pull request cannot change the third,
and the second is checks on checks.

## Where we differ from standard

| Ours                                       | Standard                                   | Piece |
| ------------------------------------------ | ------------------------------------------ | ----- |
| No delivery numbers                        | DORA's four keys                           | 1     |
| Logic in 7,170 lines of workflow           | A job calls one command                    | 3     |
| One package of 265 guards                  | Small packages split by past timings       | 4     |
| `META_PART` by hand                        | `gotestsum` timing split                   | 4     |
| Every lane runs for every change           | Path filters                               | 5     |
| Nothing requested, nothing skippable       | Labels or comments                         | 5     |
| Whole ledger on every push                 | Changed code on the PR, the full set after | 6     |
| Removal guard reruns the suite twice       | Declared dependencies checked statically   | 7     |
| Self-hosted runners on a public repository | GitHub: "almost never". Talos does it      | 8     |
| Pool proofs in the pull request            | Monitoring                                 | 9     |
| `harden-runner` on keyless jobs            | Nothing to steal                           | 10    |

## Pieces

0. **The removal lane.** Above.
1. **See it and own it.** Lead time, deployment frequency, change failure rate
   and time to restore, with definitions written first. GitHub's Actions
   Performance Metrics for jobs and queues, `gotestsum tool slowest` for tests,
   and a small scheduled check that keeps one issue open while a budget is
   exceeded. Time is state, so this reports and does not gate. The hooks are
   included: nothing measures the ten seconds today, it is a rule in a comment.
   CI times the same hooks. Every lane's limit is one declared budget, and a
   guard refuses a job with none or a limit above it.
2. **Noise and small fixes, each cheap and independent.**
   - Test fixtures write into the real job summary: `parsed.Invoke` hands a
     child the whole environment, and `retry_test.go` copies it. That is where
     the "failed attempts" come from, and the repeats.
   - Pin `ubuntu-24.04`. `ubuntu-latest` moves to Ubuntu 26, and the lock's
     Python and glibc assume 24.04.
   - One coverage line unless something changed. Raise `go/tests-helpers` from
     51.8 to 55.7, after measuring why it rose.
   - Raise the tier floors to the real counts, which had drifted to about double.
     The floors stay: the operator asked for those numbers, because the hard
     tiers are the ones that get forgotten.
   - Name the proof's steps for what they check, and agree the names first.
   - The pool proof's API step passes when `KUBERNETES_SERVICE_HOST` is empty.
   - The flake policy, Decision 5.
3. **Thin workflows.** Each job is mobilize, then one `task` command, and that
   command is what a person runs. The logic moves out of the YAML as it is, into
   a script or a verb, whichever is nearest. This epoch does not rewrite any of
   it and does not depend on retiring shell. Piece 0 follows it from the start.
4. **Split the guards** into packages by domain, then split by past timings.
5. **Asking for less and more.** Path filters, and a label to run a named lane.
   Nothing needs a label to merge. The operator, 2026-10-31: build the system we
   want, flexible enough for work to come, not what numbers happen to call for.
6. **The ledger:** changed guards and targets on the pull request, everything
   after the merge. See Decision 2.
7. **The removal check becomes static.** The whole run moves after the merge. As
   built, its cost is the suite times the applications, which does not survive
   an application menu.
8. **Compute.** Trial the heavy lanes on GitHub's runners. See Decision 1.
9. **Monitors.** Pool proofs and the Egress Proofs move out of the pull request
   until the pools retire, and every failure after a merge has a route to a
   person that cannot be missed.
10. **Every job in a catalogue:** its class (keyless, credentialed, holds a
    site's keys), its list, its schedule, its budget, its owner. The suppliers
    list already holds part of this: `egress:` for the list, and `work:` for a
    kind and how long a job takes, which `tests/go/api` compares with real runs.
    `harden-runner` leaves a job only when that job's list is replaced. The pool
    jobs can go first, because it does nothing there. Close the two wildcards on
    the builds pool, and put workflow execution protections on the list of who
    may start a workflow, if the plan allows them.
11. **A branch runner for the agent.** See Decision 6.
12. **The drill and the unattended policy.** Decision 15.
13. **Signed releases.** Moved from 08. Flux and the contractor verify before
    using one. Until then nothing proves that the digest on a site's line was
    built by the fabricator from `main`, or that the version beside it is the
    one the registry holds under that digest. Signing is a job of its own that
    sees only a digest, because the signer becomes the thing to protect. A new
    supplier (cosign), so its own pull request.
14. **A site's pin moves itself.** Moved from 08. Self-hosted Renovate opens the
    one-line change in `management/versions.json`; it replaces the fabricator's
    delivery job and procurement's `deliver`, and the 4am standing order points
    at what Renovate opens. See Decision 7. Waits on piece 13 and on the roll
    being proved. It is the same Renovate that 06's criteria 1 and 2 want for
    dependency bumps, so it is set up once: Dependabot leaving stays on hold,
    but nothing here should stand in its way.

15. **The bake.** Decisions 9, 10 and 11: the post-merge stage, the required
    check, and Flux following the release.

**Order:** 0, then 2, 1 and 3, because those stop the bleeding and make the rest
measurable. Then 4 to 7, which take the most time off, and 8 and 9. 13 and 14
own the headline number, and can run beside any of these once the roll is
proved.

Each piece names what it takes off the critical path, measured before and
after. One that takes nothing off says it is a cleanup.

## Decisions

### 1. Where keyless work runs

**Chose A,** agreed 2026-10-31, subject to the trial on invariants.

**A. Every keyless job on GitHub's runners, sealed.** Free, unlimited, 4 vCPU
and 16 GB, no queue. One reading on 2026-10-23 had invariants at 525 s there
against 1,248 s on the pools. Tools delivered by hash, then the check runs with
no network, which is an allow list of nothing. Pull-request code leaves the
estate's hardware. The pools, the untrusted worker, the sweeper's wipe and the
pool proofs can go, and 14 GiB comes back. Setup stays open, where the pools
hold a whole job to a list, and it runs only pinned actions and hash-checked
downloads.

**B. Light lanes on the pools, heavy on GitHub.** Keeps a list over setup. Two
worlds to run, and the pools stay.

**C. Everything on the pools, on a bigger machine.** About 28 GiB and 25-plus
cores against 14 GiB and 8 today, on an 18-thread host. It does not fit.

Some lanes need the network mid-run, such as Trivy for its database, so each
says what it fetches before the seal. Before retiring the untrusted worker, note
the roll uses it as a canary (05).

### 2. The full ledger does not hold a merge

**Chose:** after the merge, with changed proofs on the pull request. The queue
runs the whole commit stage with no path filters. Agreed 2026-10-31.
**Because:** standard and fastest. A bad guard can reach `main`, and is found in
about half an hour, and the gate in Decision 10 then closes.

### 3. Whether results are reused (open)

Go's cache is standard and is off: it gave a false pass, so `-count=1` stays
until the cause is known. Path filters come first; Bazel only if they fall
short, because it is the heaviest migration on the list. Recording what each
guard reads through `tests/parsed`, the design of 10-25, is ours to own, so it
is the last resort rather than the first.

### 4. Thin by hand, or generated (open)

Talos generates its workflow from config with `kres`, its own tool, and nobody
edits the result.

**A. Thin and hand-kept.** Once a job is one command, little repeats.

**B. Generated from the catalogue in piece 10.** One declaration, and egress and
workflows cannot drift. Another thing we own, and the output is still reviewed.

**Recommend A,** and look again if the catalogue and the workflows keep
drifting.

### 5. What happens to a test that fails by chance (open)

The estate's rule is no retries, and `/retest` and Ansible's `unstable` exist
because flakes are real. Neither is a policy on its own.

**A. Rerun the lane by hand.** Standard and easy, and it hides flakes.

**B. A flake is a defect.** It gets an issue, and the test moves to nightly
until it is fixed. A runner that died is a different thing, and rerunning that
is fine.

**Recommend B.** It keeps the estate's rule and stops one test holding a merge.

### 6. How the agent gets a test run (open)

The main author's box has 3 GB of memory, so it cannot run the heavy suite and
uses CI as its test runner. Every push costs a full run. Nobody else's pipeline
has this problem, and it may cut more load than any lane split.

**A. A label or commit keyword** that narrows a push to named lanes.

**B. A dispatched run of one command on a branch.** Clean, and needs a
permission the App does not hold.

**C. A bigger box.** The simplest, and the heavy suite then runs off CI.

**Recommend A,** which piece 5 already builds.

### 7. Who moves a site's pin

Two records disagree, and this decides the headline number.

**A. It moves itself.** The ruling of 2026-10-06: the bump auto-merges as long
as there is an auto-revert, and once one fires it switches to manual until
resolved. Merging stays the operator's only input.

**B. A person ticks a dashboard.** The design carried in 08: the promotion pull
request is opened on request, and nothing is opened after a merge. Safer to
read, and every release waits for a person.

**Chose A,** with a per-site hold. The roll is what makes this safe, so a bump
is not stopped for changing machine versions. What stops it is the one
unattended policy in Decision 15.

### 8. What reverts by itself

**Chose:** a converge that provably touched nothing reverts by itself, which is
the exit-code path that already exists. A release that converged and then
alerted rolls back under the unattended policy in Decision 15. A converge that
failed partway opens an issue and never a revert, because nothing is known about
what it wrote.

### 9. Nothing is run twice for the same tree

**Chose:** no merge of `main` into a branch. A `pull_request` run already checks
out `refs/pull/N/merge`, so it tests the branch merged with `main` as it stood.
`already-tested` compares the queue's tree with the branch's own tree, which is
why it rarely hits. The fix: each run records the tree it tested, and the queue
compares against that. The queue reruns only if `main` moved after that run,
which is the one case worth rerunning, and it costs no push and no approval.

### 10. `main` bakes, and a failure closes it

The operator, 2026-10-31: code sits in `main` while checks run against it,
nothing is elevated until they finish, and a failure is a failing check in
GitHub that stops merges.

**After a merge, not nightly.** The ledger, the removal check and the sweeps run
on each commit to `main`, as Kubernetes' postsubmits do, so the answer comes in
about half an hour and not a day. Nightly keeps what truly needs a schedule.

**Promotion waits for that answer.** Renovate holds the pin until the commit's
checks pass. `minimumReleaseAge` is its built-in form, reported as
`renovate/stability-days`, but it counts time and not results.

**The gate is one required check in every pull request and the queue.** It reads
what is failing on `main`.

**The trap is a deadlock:** the fix has to merge too. So the check reruns
exactly the failing checks on the pull request's tree. One that clears them
passes; any other fails.

**A failing monitor cannot be cleared by a tree.** **Chose:** it holds the pin
and alerts, and merges go on. Agreed 2026-10-31: an unrelated pull request must
be able to land, and so must a fix.

**This needs manifests to ride the release.** See Decision 11.

### 11. Flux follows the release, not `main`

**Chose A,** agreed 2026-10-31.

Today Flux applies `clusters/**` within about a minute of a merge, so `main` is
not staging for anything in that path: it is live before anything bakes.

**A. Flux reads the signed release a site pins.** `main` then really is staging.
One pin moves everything, so a revert undoes manifests too, the trap of a
manifest arriving before its release is gone, and the gap in signing closes.
Sites already read applications this way. Costs: a manifest change waits for the
bake, and a hotfix needs a fast path, which is a pin moved forward early.

**B. Leave it.** Baking covers modules only.

### 12. Machines roll at any hour

**Agreed 2026-10-31,** once the two things below are answered.

No reason for the 02:00 to 06:00 window is recorded, and the operator asks why
it exists, since cordon and drain keep the site at capacity. Drain does not save
a single-replica workload: a game server drained at 2pm drops its players. So a
workload declares that, and the roll waits for it to be free rather than for an
hour. And a roll holds the converge's lock for up to 180 minutes, and GitHub
keeps one waiting run, so a daytime roll can cancel a waiting converge. 05
records that queue as not built. It has to be.

### 13. A hotfix

Measured on 2026-10-31: the fabricator takes 1.5 minutes at the median and 4.9
at worst, a converge 4.1 and 13.4, and a pin's pull request 40 to 54 today.

**Back first.** When the last release is the problem, moving the pin back to the
previous digest needs no code: it is signed and has already baked. Fix forward
afterwards.

**Forward.** The required checks are not shortened, because a hurried fix is
when they matter most. Three things change:

- A pin's pull request runs only what a pin can change: the plan. The code in it
  passed in `main`. This is the one place a path decides lanes, and it is safe
  because nothing but a line in `management/versions.json` differs.
- There is no hotfix mark. A label is something the agent's App could set, and
  it would skip the bake on a change the operator believes is ordinary. The
  machine decides instead: while a site alert is open, a commit merged after the
  alert fired may pin without waiting for the bake. The bake still runs behind
  it. A bad actor would need a real alert, the operator's approval and every
  required check, and the pin must clear the alert or it is rolled back.
- A held pin must still let this through. A failing monitor holds ordinary
  promotion only; otherwise the hold would stop the fix reaching the site.
- Renovate is asked at once rather than on its next run.

**Expected, not measured:** about 13 minutes from push to `main` when `main` has
not moved, and about 25 to running on a site.

**A broken check cannot trap a fix.** A pull request runs its own workflow, so a
fix to a check is judged by the fixed check. Workflow changes are still applied
by the operator. If GitHub is down, a converge runs from the workstation, as it
can today.

### 14. What the operator does (punted 2026-10-31)

Neither recommendation below is agreed. Both wait.

The aim, the operator, 2026-10-31: the agent opens a pull request, the operator
approves it and sets it to merge, and everything else is a machine. Today there
are two more acts: elevating out of draft, and resolving the sensitive-path and
key conversations.

**Elevating.** Draft exists because the agent cannot be trusted to say it is
done. A machine can say so instead: every required check green marks the pull
request ready, and the clerk reads it then. **Recommend this.**

**The sensitive-path conversation** was built for two people, and there is one.
It opens on most pull requests, so it is a click nobody reads. The list can go
in the approval page and the required check can pass by itself. **Recommend
this.**

**The key conversation** stays. It opens only when a job is handed something it
did not hold, which is rare and is the thing to look at.

### 15. Time to restore is a number in code

The operator, 2026-10-31: a set time, kept to in code, for a roll-back and a
roll-forward.

**The clock** starts when the alert fires and stops when it clears.

**The number is declared once,** with a budget for each stage. The stage budgets
must add up to it, each job's `timeout-minutes` is its stage's budget, and a
guard refuses any that disagree. That is the declaration.

**The number is 10 minutes,** agreed 2026-10-31 as the target for a
change-caused incident. From measured parts a roll-back takes about 9 at the
median and 18 at worst, so the budget is: decide 0.5, pin 2, queue 1, Flux 1,
converge 5, with 0.5 spare. Three things have to be true first:

- A pin that goes back needs its checks in 2 minutes, and all required checks
  must still report. Only the plan does real work.
- Flux following the release, so manifests go back in about a minute.
- A converge inside 5 minutes. It is 4.1 at the median and 13.4 at worst, and
  why the slow ones are slow has not been looked at. Until that is fixed, the
  drill will breach the budget, and that is what it is for.

**A person cannot be budgeted, so the roll-back is automatic.** An alert within
a set time after a pin moves the pin back to the previous baked release, with
nobody involved. One automatic roll-back latches: nothing rolls back or forward
by itself again until a person clears it. That is what makes the number true:
the site is restored inside it, and the fix forward then has no clock on it.

**The target is bounded.** Only the previous baked release, so a roll-back
cannot be pointed at an old release with a known flaw.

**A roll-forward's bound is the pipeline's:** about 35 minutes from push to site
at worst, 25 at the median. Writing the fix cannot be budgeted. It applies alone
when going back is impossible.

**One rule decides anything unattended: is a person needed?** A roll-back goes
ahead unless it could be dangerous without one. The same rule covers a pin's
auto-merge (Decision 7) and a revert (Decision 8), so there is one policy,
judged by Conftest on OpenTofu's plan.

It starts from nothing: the plan's actions must all be on a declared list of
changes safe to make alone, and anything else stops for a person, including what
nobody thought of. Agreed in principle 2026-10-31; the list is to be written.
Off the list from the start:

- destroying or replacing a machine, or a volume, or anything holding data;
- the cluster's secrets, or its certificate authorities;
- etcd's membership;
- a database's major version, or a change it cannot undo;
- crossing a Talos minor version;
- a site already unhealthy in a way the change depends on: etcd not voting, or
  the database short of instances.

A latch set by an earlier automatic action also stops it.

**What it cannot cover:**

- Causes that are not a change: hardware, a vendor. Rolling back does nothing.
- Machine versions. A roll can run 180 minutes, and Talos goes back within a
  patch only. They need a number of their own.
- Data changes, which do not go back.

**Proved, not assumed: the drill.** On a schedule, weekly to start. The
fabricator publishes a test release: signed, and carrying nothing but a marker.
It is pinned, a test alert fires, the machinery rolls it back, and the clock is
read. Over budget is a failure. It uses site0, because there is no staging site,
and it moves a real pin twice. It stands aside if a real pin, an open alert or a
roll is under way, and it is left out of the delivery numbers. Every real
incident is timed too, and a breach opens an issue.

## Depends on

- **#772's follow-ups:** the roll proved on site0, for piece 14, which depends
  on machines being rolled safely once a pin moves.
- **Alerts on a failed roll and a failed converge,** which auto-revert reads.
  Epoch 04 built them; piece 14 confirms they cover this.

## Acceptance criteria

1. Lead time from merge to running on a site is measured and has a target.
2. Deployment frequency, change failure rate and time to restore are measured.
3. A pull request's required checks finish within ten minutes of a push, with
   no queue wait, on a typical change.
4. Each budget is measured and an alert fires when it is exceeded.
5. No lane sits within a fifth of its limit.
6. Commit and push stay under ten seconds.
7. The job summary carries nothing a fixture wrote and no repeated section.
8. Every job has one class, one list, one schedule, one budget and one owner.
9. A check that moved off the pull request still runs, and its failure reaches
   a person. Checks moved are not checks lost.
10. A job's body is one command, and a person can run that command.
11. `harden-runner` is gone, and no job lost its list to get there.
12. A release is signed, and Flux and the contractor refuse one that is not.
13. A merged platform change reaches a site with no hand on the pin, and a
    failure rolls it back and latches to manual until resolved.
14. A failure after a merge fails a required check, and a pull request that
    clears it can still land.
15. A change-caused incident is restored within the declared ten minutes, proved
    by a drill on a schedule.
16. Nothing unattended happens unless its plan is wholly on the declared list.
17. Security posture has not gone backwards at any piece, stated in each.

## What stays ours

The guards, the Conftest and Semgrep rules, a changed-only ledger (no standard
mutation tool targets policy rules), the contractor and `plan-as-built`, whose
reasons are in epoch 04.

## Deferred

- Bazel, unless path filters and packages fall short.
- Refining the clerk's prompt for three false findings on #772.

## Gotchas

- A guard that reads the whole repository depends on everything, so path
  filters must not narrow those; they need their own trigger list or nightly.
- A required check that is skipped must still report, or the queue waits on it.
- Workflow changes are the operator's to apply, as patches, and a change to a
  pool's policy cannot pass its own check.
- A label must never be required. Merging stays the operator's only input, so a
  pull request with no label gets the safe default.
- Whatever chooses the lanes is a command too. Change detection written into
  workflow YAML is the pattern this epoch is removing.
- A path filter can under-select by mistake. So the queue ignores them, if we
  have any. A pull request changing its own checks is not the concern: that is
  settled by code-owner review (#827).
- Where the latch is stored, what clears it, and that Renovate reads it, need
  building.
- A pin reverts what the release carries. Until Decision 11 lands that excludes
  manifests, and a manifest can arrive before what it needs. Machines go back
  within a patch and not across a minor, and data changes do not go back at all.
- Lead time has two clocks, modules at the converge and machine versions at the
  roll, and now a bake in front of both. Define them in piece 1.
- A converge builds the contractor from `main` while the roots use the release's
  modules. They can differ for as long as a pin lags.
