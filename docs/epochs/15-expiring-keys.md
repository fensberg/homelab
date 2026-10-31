# Epoch 15 — Expiring Keys (working title)

- **Tier / path:** `management/`, `clusters/core/`, `scripts/contractor/`,
  `.github/workflows/`
- **Branch:** one per piece, from `main`
- **PR:** #
- **Status:** Proposed. Next, with [16](16-fast-feedback.md). Nothing here is
  built, and nothing is built until the decisions below are made.
- **Closed:**

## Goal

A site's credentials stop living in GitHub. A job that needs one proves who it
is, is handed a credential that expires by itself, and every hand-over is
logged. A copy that leaks afterwards is worth nothing.

This is the step the 2026-10-31 audit of pull-request CI led to, and it is the
decided entry in [ideas.md](../ideas.md), "1Password for ignition, OpenBao for
everything else". That entry says the hard part is bootstrap and that it
should be designed first. This record is that design.

The rule it is held to, from the operator: the structure can change, and the
security posture must only improve. Each piece below says what it improves and
what could get worse.

## Scope

In scope:

- OpenBao for each site: where it runs, how it unseals, how it is backed up,
  how it comes back after a total loss.
- How a job on a site's runner proves who it is.
- `OP_SERVICE_ACCOUNT_TOKEN` leaving the `management` and `integration`
  environments in GitHub.
- A hold on where the jobs that receive those credentials can connect (#411).
- The first credentials that expire: Postgres, then object storage where the
  vendor allows, and Talos and Kubernetes client certificates.

Explicitly out of scope:

- The estate's own credentials. They stay in 1Password and the `estate`
  environment; see "The estate's keys do not go on a site".
- Actions as the scheduler. It stays. Whether a site should pull a signed
  snapshot instead is decided when this closes, on what it would still add.
- CI speed, sealed test lanes and removing `harden-runner`:
  [16](16-fast-feedback.md).
- The 12 jobs on GitHub's runners that hold a secret or a write token. None
  holds a key to a site, and each gets its own answer in 16.
- Signing releases and moving a site's pin: 16, moved there from 08.
- Workload secrets beyond what proves the path (#345, #520). They follow once
  the path exists.

## What is true today

- Everything a converge reads comes through `op inject`, from about 116
  `op://` references in the config template, workflows and the roll.
- The token behind them is long-lived and sits in GitHub, scoped by
  `scripts/details/vaults`: read and write on `<site>`, read on `<site>-shared`
  and `estate-shared`. The contractor refuses a wider one.
- Five jobs use it: converge, roll, roll-handover, integration tests and the
  estate's converge. All run on the estate's runners, where `harden-runner`
  does nothing, so nothing limits where they connect (#411).
- The cluster credential the roll uses is issued per run and deleted after it,
  but no lifetime is set. Deleted is not worthless.
- A pull request's plan holds no credential. That stays exactly as it is.

## The principle

Standing rule, and already an invariant in `CLAUDE.md`: what we protect should
be worthless if taken.

- **Expiry beats rotation.** Rotation after a leak loses to someone who uses
  the key in seconds. A key that dies by itself does not depend on noticing.
- **Encryption counts only if the key to it is out of reach of the same
  compromise.**
- **Something at the root keeps its value.** Keep that set small, name it, and
  keep it out of automation.
- **A live key can still act during its window.** Allow lists limit what
  happens in the window, and expiry ends it. Neither replaces the other.

## The loop

1. A person seeds a value at ignition, from 1Password.
2. OpenBao replaces it at once. The database engine's `rotate-root` exists for
   exactly this: after it, only OpenBao knows the password.
3. Jobs get leased credentials. Each dies at the end of its lease.
4. The seed is dead, so deleting it from 1Password loses nothing.

**Not every secret can do this.** That is the most important limit in this
design.

| Class                                     | The loop                                                                                       |
| ----------------------------------------- | ---------------------------------------------------------------------------------------------- |
| Postgres                                  | Works. Documented: root rotation, dynamic roles, static roles. The acceptance test             |
| Kubernetes, SSH, internal certificates    | Should work. Engines exist; to confirm on our version                                          |
| Talos client certificates                 | Works in principle through the PKI engine, which puts the Talos CA in OpenBao. Decision 5      |
| State encryption                          | Stays in 1Password: a rebuild needs it before OpenBao exists. Decision 2                       |
| Object storage, tunnel, DNS               | Partly. Cloudflare has a roll API and no engine, and a bespoke scheduler is ruled out by ideas |
| Proxmox token                             | Not yet. No expiry by default. A regenerate option was proposed in April 2026; unconfirmed     |
| GitHub App key, other console-issued keys | No. `CLAUDE.md` calls this the honest floor                                                    |
| The age break-glass identity              | Never, deliberately                                                                            |

What cannot rotate moves to OpenBao as a stored value only if that is better
than where it is, and stays in 1Password if not.

## Decisions

### 1. The estate's keys do not go on a site

**Chose:** one OpenBao per site, holding that site's scope only. The estate's
credentials stay in 1Password and the `estate` environment, as #345 says.
**Because:** a site is the isolation boundary and the estate is the blast
radius. `scripts/details/vaults` exists so that a site's compromise is not the
estate's. An estate credential in a site's OpenBao would undo that.
**Rejected:** moving everything to OpenBao and retiring 1Password. Ignition
needs it before any cluster exists, and an estate-level OpenBao needs somewhere
outside any site, which is the rented server deferred until a second site is
real. Revisit then.

### 2. Where OpenBao runs

**Chose:** in the cluster, three replicas with Raft on local volumes, spread
across the workers and reconciled by Flux, with a disruption budget so the roll
takes one at a time. The operator, 2026-10-31: it has HA of its own and needs no
VM. **Because:** no new kind of machine, no new network path to Postgres,
ordinary Kubernetes auth, and no pipeline of its own to maintain. **What it
costs:**

- It shares the cluster's fate, so the state encryption key stays in 1Password's
  bootstrap set: a rebuild needs that key before any cluster exists.
- HA here covers a machine or a pod, not the hypervisor, like everything else on
  a one-node site.
- Three replicas on workers already short of memory. Measured in piece 0.

**Worst case:** cluster-admin can impersonate a job and take any lease. That is
true of every option that serves this cluster. Policies limit it and the audit
log shows it. **Rejected:** a VM of its own, and the hypervisor. Each is more to
run for protection against an attacker who, holding the cluster, already gets
leases.

### 3. How it unseals (open)

Every replica must unseal itself, because the roll restarts each one, so a
person cannot do it.

**A. A static key in a Kubernetes Secret.** Unattended, and no new vendor. The
key lives in etcd on the control planes and the data on the workers' volumes, so
one stolen disk or backup does not hold both. It does not hold against
cluster-admin, but cluster-admin already gets leases.

**B. A cloud KMS plugin.** About $1 a month on AWS within its free requests, and
revocable from outside. A new vendor, a credential of its own, and an unseal
needs the internet. Behind a strict limit.

**C. A second OpenBao for transit.** The usual homelab answer, and it needs its
own unseal, so it moves the problem.

**Recommend A,** with B to come if a real need appears. Before relying on A,
check what backs up etcd, because a backup holding both would break the
argument.

### 4. How a job proves who it is

**Chose:** Kubernetes auth. A runner pod gets a short-lived projected service
account token, bound to an audience, and exchanges it for a lease. Each scale
set gets its own service account and OpenBao role.
**Because:** nothing is stored. It is the secret-zero answer the cluster
already provides.
**Worst case:** anything that can run on that runner gets that role's lease.
GitHub still decides what runs, so a bad merged workflow can ask for a key. It
gets a short-lived one, limited by policy and logged. Closing that is what a
signed snapshot would add, and it is the question this epoch ends on.
**A job is still one command.** The exchange happens inside the program the job
calls, not in workflow steps, so the job stays thin and a person can run the
same command. That is what keeps the snapshot decision cheap: a job that is one
command from a signed release can become a Kubernetes Job without a rewrite.
**Rejected:** GitHub OIDC to OpenBao. It needs OpenBao reachable from GitHub,
and nothing on the estate listens.

### 5. Where the Talos CA goes (open)

**A. Stays in state.** A client certificate is still issued per run, with a
short lifetime set. Small change, no new risk.

**B. Moves to the PKI engine.** Short-lived certificates with no reachable CA
key. Better, but only secondary sources describe it for Talos.

**Recommend A now, B as a later piece** once it is proved on a scratch site.

### 6. What gets the token out of GitHub (open)

The render is the catch. `op inject` needs a token, so the GitHub secret cannot
simply be deleted while the template reads 1Password.

**A. Keep `op inject`, and hand the token out through OpenBao.** The job fetches
the 1Password token per run. Small change. It leaves GitHub, but stays
long-lived, so a leak from a job still works.

**B. Render from OpenBao.** The same template, with each reference read from
OpenBao when a job runs and from 1Password at ignition. The right end state,
and every value is at last short-lived or audited. Needs a small reader in the
contractor, and two stores can drift.

**Recommend A first, then B secret by secret.** A proves the auth path and
removes the GitHub secret early. B follows until the token has nothing left to
read. Ignition keeps `op inject` throughout.

### 7. How a pod gets a secret (open)

**A. External Secrets Operator.** Named in ideas and #345. Its OpenBao provider
reads KV and supports Kubernetes auth. We found no evidence it serves dynamic
credentials.

**B. The OpenBao Secrets Operator.** Claims every engine. Not evaluated.

**Recommend deciding at the Postgres piece, on what a real lease needs.** Jobs
call OpenBao directly and need neither.

## Recovery

- **A leaked lease:** expires. Revoke early if needed.
- **A replica lost:** the others carry on, and it rejoins.
- **Every replica and its volume lost:** restore the newest snapshot. A snapshot
  holds credentials that have since rotated, so restoring is followed by
  reseeding what it no longer matches. Backups are encrypted to the estate's age
  recipient, the existing pattern: the automation writes what it cannot read.
- **Total loss of the site:** ignition, unchanged, from the bootstrap set in
  1Password. Which values stay there is listed before the first secret moves.
- **Recovery keys** are encrypted to the same recipient at initialisation and
  never shown to anything else.

**OpenBao being down fails closed.** Converge and roll stop, and leased
credentials expire under whatever holds them. Lease lengths must outlast a
realistic outage, and the watchman raises it from outside.

## Build order

Each piece is one pull request and leaves the estate better than it found it.

0. **Spikes, no estate change.** Talos on the PKI engine, memory use under load,
   what backs up etcd. Results here.
1. **Short lifetime on the roll's cluster credential.** Worth doing whatever
   else is decided.
2. **#411: a hold on the key-holding jobs.** Cilium, deny by default, per scale
   set. Before any key arrives by a new route. Also answers whether the trusted
   side has any default deny, which is unconfirmed.
3. **OpenBao standing, holding nothing.** Replicas, seal, TLS, audit device,
   backups, a restore proved on a scratch instance, the watchman.
4. **The acceptance test.** A Postgres credential minted on demand, leased, and
   revoked automatically.
5. **Auth path and Decision 6 A.** The GitHub secret goes from `management` and
   `integration`.
6. **Render from OpenBao,** one class at a time, until the 1Password token on
   the runner has nothing left to read and is deleted.
7. **Decide on the signed snapshot.**

## Depends on

- [Epoch 16](16-fast-feedback.md), and first of all its stopgap: the removal
  guard's own lane. Every pull request here lands through that pipeline, and
  today the invariants lane times out by chance.
- #772's follow-ups: site0's pin and the image pins moved, and the roll proved.
  OpenBao arrives after machines can be rolled safely.
- #767 merged, and the capacity it describes understood.
- A supplier pull request for OpenBao, on its own.

## Acceptance criteria

1. No site credential is stored in GitHub.
2. A Postgres credential is minted on demand, carries a lease, and is revoked
   automatically when it ends.
3. A job's credential is useless after the job. Proved by using one afterwards.
4. Every hand-over is in an audit log kept off the OpenBao machine.
5. The jobs that hold keys can reach only what they declare.
6. OpenBao restores from its backup on a scratch instance, and rotated values
   are reseeded.
7. Ignition of a scratch site works with OpenBao absent.
8. The roll runs with OpenBao on the estate, and no step needs a person.
9. A stolen copy of OpenBao's volumes and backups yields nothing without the
   seal.
10. A pull request's plan still holds no credential.

Tests hold declarations; the watchman and alerts watch state. Each criterion
that is a declaration gets a guard and a proof in the ledger.

## Checked before building

| Need                   | Tool                                        | Result                                      |
| ---------------------- | ------------------------------------------- | ------------------------------------------- |
| Secrets, leases, audit | OpenBao                                     | Decided in ideas                            |
| State key              | OpenTofu `openbao` key provider             | Native                                      |
| Into Kubernetes        | External Secrets Operator, OpenBao operator | Decision 7                                  |
| Egress                 | Cilium                                      | Running                                     |
| Unseal                 | Static seal, KMS plugins, transit           | Decision 3                                  |
| Rotation schedule      | OpenBao's own                               | Nothing bespoke. Ruled out in ideas         |
| Omni                   | Rejected in 05                              | Holds every machine's credential; unchanged |
| Pull-based tofu        | `tofu-controller`                           | Only if the snapshot goes ahead             |

## What could get worse

- **A new root.** OpenBao and its seal become the valuable thing. Named here,
  deliberately small, backed by the age key.
- **A new dependency.** If it is down, deploys stop. Accepted: it fails closed.
- **Quorum during the roll.** The play must wait for OpenBao to be healthy
  between machines, as it does for etcd.
- **Two stores during migration.** Each secret has one owner at a time, listed
  in this record, and a guard holds the list.

## Deferred

- The signed snapshot, which needs 16's signed releases, one signed source for
  manifests, a home for the as-built record, an alarm on silence, and 16's thin
  jobs.
- An estate-level OpenBao, when a second site makes it worthwhile.
- GitHub's network firewall, which only logs today.

## Gotchas

- Rotated values no longer match 1Password. A rebuild that reads an old seed
  gets a dead value, which is why the bootstrap set is listed first.
- A restored snapshot brings back credentials that have since rotated.
- `rotate-root` must not be pointed at a static role: OpenBao's docs warn it
  breaks every user under that connection.
- Flux follows `main` and a site runs its pinned release, so a manifest may not
  use something until every site's release writes it.
- A short lease is 15 minutes, renewed by the job while it lives, capped at the
  job's limit, and revoked when it ends. A job can run 180 minutes on one, and
  one that dies leaves a credential with 15 minutes on it at most. The roll and
  a converge renew from inside their one command.
- Do not give OpenTofu's Postgres backend a dynamic role. Objects and locks
  would belong to a user that is revoked. Use a static role, rotated by OpenBao;
  the dynamic acceptance test is for something else's credential.
