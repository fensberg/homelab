# Epoch 08 — Agent Roles

- **Tier / path:** `.github/`, `scripts/`
- **Branch:** `epoch/08-agent-roles`
- **PR:** #
- **Status:** Not started — deliberately parked

## Goal

Give the second model a job it is actually better at than the first, and write
down why that division holds. At the end there are two automated roles with
different information, different outputs and different owners, and a record
that says what each is for — including what was tried and cut.

Parked rather than started. It is the most immediately interesting thing on
this list and the least load-bearing, which is exactly the combination that
pulls attention off unfinished work. Epoch 01 has to close and the self-hosted
runner has to carry a converge before any of this begins.

## The division that makes it worth doing

One model holds the context; the other deliberately does not. That is the whole
design, and it decides what each may be asked.

**Context compounds** for the agent that works in this repository. Finding why
the overlay was dead took five hypotheses eliminated by measurement, and each
elimination depended on knowing what had already been ruled out and why. No
amount of fresh perspective substitutes for that.

**Context disqualifies** for a handful of tasks where knowing the intent is the
thing that stops you seeing the artefact. Those are rarer than they sound, and
naming them precisely is most of the work of this epoch.

## Scope

In scope, in the order they earn their place:

- **The planner — a sweep over deferred triggers.** Every Deferred entry in
  these records carries an explicit trigger, and nothing checks whether any
  have fired. At least two had before anybody noticed: self-hosted Renovate
  ("once the cluster is up") and a persistent runner tool-cache ("once
  self-hosted runners exist"). This reads English, needs no code context, and
  produces issues somebody triages. It is the cheapest useful thing here.
- **The clerk of works — an independent reader with no stake in the build.**
  A clerk of works inspects on the client's behalf, separately from the
  contractor; the name is a real role rather than a label. Two jobs where
  having no context is a genuine advantage:
  - **A forkability audit.** The repository is meant to be clonable by a
    stranger who points it at their own vault. `tests/go/repo/forkable_test.go`
    enforces that by pattern and catches literal names; it cannot catch a step
    that assumes an account already exists, a runbook missing a prerequisite,
    or a config key with no instruction for what to put in it. Reading as a
    stranger _is_ the task, so context disqualifies.
  - **Claims versus diff.** Does every assertion in a pull request body have
    evidence in the change? This needs the body and the diff and nothing else,
    and it catches a description that oversells or describes an earlier
    version. Pull request bodies in this repository are long and confident,
    which makes the failure mode plausible rather than theoretical.
- **The inspector — a conversation per file, not a summary of what tripped.**
  The sensitive-path gate opens a review conversation anchored to a file in the
  diff, and today it posts one comment listing which rules fired and which
  files matched. That listing exists because the comment is anchored to one
  file and has to account for the others: it is a workaround for having only
  one place to speak.

  A conversation on each affected file removes the need for it. Anchored to the
  file, "what triggered this" is self-evident from where the comment is, and
  the space that the rule listing occupies becomes available for something
  worth reading.

  That is what makes this epoch's work rather than epoch 01's. The gate can
  already open a conversation; what it cannot do is say anything a reviewer
  did not already know. An agent with the diff can say **what changed in this
  file, why it appears to have changed, and how it could go wrong** - which is
  the whole purpose of the pause, and is currently supplied by a static
  sentence written months earlier in `.github/sensitive-paths`.

  Constraints inherited from the mechanism, all of them already established
  and none of them up for renegotiation here:

  - **A machine may not resolve the conversation.** `scripts/attestation`
    refuses a resolution by a bot or by the pull request's author, and an
    agent explaining a change is emphatically not the party who signs it off.
    The inspector writes; a human resolves.
  - **The conversation is keyed to a digest of the sensitive part of the
    diff**, so an acknowledgement cannot outlive what it acknowledged. Per-file
    conversations mean per-file digests, so changing one file must not
    invalidate the acknowledgement given for another.
  - **No trigger fires on a conversation being resolved.**
    `pull_request_review_thread` is a webhook event that was never ported to
    workflow triggers, so nothing re-runs a check when somebody resolves a
    thread. Anything built here must never block on a state only a human
    action can clear.
  - **The output is structure, never a value.** A pull request comment on a
    public repository is world-readable, and the estate keeps hostnames,
    addresses and credentials out of git deliberately.

  Raised while looking at a real comment on #163: the rule listing is doing the
  work an anchor should be doing, and the reason it cannot be dropped yet is
  that nothing else would be there.

- **The record itself**, naming the division and what was rejected.

Explicitly out of scope:

- **A persona matrix reviewing every pull request.** Rejected, with reasons
  below.
- Anything that opens a pull request rather than an issue. An issue is cheap to
  ignore; an unmerged pull request is a standing request for review time and
  becomes noise on a board that is deliberately kept short.

## Decisions

### Rejected: three zero-context personas on every pull request

**Chose:** two narrow jobs where the absence of context is an advantage.
**Rejected:** parallel security, quality and architecture reviewers on each
pull request.
**Because:** correctness in this repository is substantially contextual, so a
reviewer without the records is confidently wrong about things that were
decided deliberately. Three examples, all current:

- IPv6 disabled at the stack reads as weakening a host. It is the fix for a
  total overlay outage.
- `insecure = true` on the hypervisor provider reads as a glaring hole. It is a
  recorded deferral whose trigger is a trusted certificate.
- A routing rule at priority 5200 reads as arbitrary. Its priority is the
  entire fix.

Two of those three are wrong findings, and wrong findings are expensive here
specifically: the operator relies on the checks because they are not in a
position to adjudicate a code claim from the diff. A reviewer that is
confidently wrong a third of the time inverts what makes the lanes valuable.

Two further objections come from rules this repository already has. **Each
check has exactly one owner**, and three personas overlap Semgrep, Trivy,
CodeQL, Checkov, TruffleHog, Zizmor and Super-Linter without owning anything.
And a deterministic scanner that is wrong is wrong the same way every time, so
it can be tuned or exempted; a model is wrong differently on each run, so no
suppression list is possible and a change in its output cannot be
distinguished from a change in the code.

### The reviewer is reserved for epoch pull requests

**Chose:** run the clerk on `epoch/**` → `main` only.
**Because:** the cost is per-run and metered, and a small pull request does not
need an independent reader. The epoch pull request is where a lot of work
lands at once and where a second reader is worth paying for.

### Issues, never a branch

**Chose:** the planner opens issues; neither role commits.
**Because:** an agent that writes code here without reading the records
produces work that fails the gates and, worse, work whose reasoning is never
written down. That was demonstrated rather than assumed: a first attempt at
this integration arrived as a pull request that failed six lanes including
three security scanners, because it did not know that new logic goes in Go,
that workflows are a protected path, or that the estate pins what it runs.

### Spend is bounded like every other metered vendor

**Chose:** `timeout-minutes`, a `concurrency` group, bounded retries, and a
schedule no more frequent than the question needs.
**Because:** this is a metered API reached from a scheduled job, which is the
shape the building code already governs. The endpoint also has to be declared
in `scripts/approved-suppliers.yml`, because egress is deny-by-default and one
file answers what this repository may call out to.

## Acceptance tests

1. **The planner finds a trigger that has genuinely fired**, and the issue it
   opens is one somebody acts on rather than closes as noise.
2. **The clerk finds a forkability failure that
   `tests/go/repo/forkable_test.go` cannot express** — a semantic assumption
   rather than a literal name.

If either role cannot pass its test, it is decorative and comes out. A
mechanism whose output nobody acts on is worse than no mechanism, because it
looks like coverage.

## Outcome

Not started.

## Deferred

- **Anything on small pull requests.** Trigger: the epoch pull request review
  proving useful enough to be worth the cost more often.
