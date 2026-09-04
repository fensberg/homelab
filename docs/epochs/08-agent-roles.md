# Epoch 08 — Agent Roles

- **Tier / path:** `.github/`, `scripts/`
- **Branch:** `epoch/08-agent-roles`
- **PR:** #
- **Status:** In progress — started 2026-09-04

## Goal

Give the second model a job it is actually better at than the first, and write
down why that division holds. At the end there are two automated roles with
different information, different outputs and different owners, and a record
that says what each is for — including what was tried and cut.

This was parked on two conditions, and both have now fired: epoch 01 signed
off on 2026-09-03, and acceptance test 2 — a merge-driven converge with nobody
at a terminal — passed on 2026-09-02 from the self-hosted runner. Started
2026-09-04 on that basis rather than by overriding the deferral.

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
  - **A handover audit.** The repository is meant to be clonable by a
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

### The inspector owns attestation, and `scripts/attestation` is deprecated

**Chose:** the sensitive-path gate moves into `scripts/inspector` as a verb.
`scripts/attestation` is deprecated and removed once it has.
**Because:** they are one role in two programs, which is the bloat this estate
refuses everywhere else. The decision below kept the gate "a role rather than
an identity" and named the trigger that would change that: a second job. That
job exists - `tally` reads a change and reports what it takes away, and has been
running on every pull request since it landed. The trigger fired.

The operator's framing settled it: "We're de-bloating and that includes
redundant roles." Two zero-dependency Go programs, both acting on a pull
request, both the internal party that inspects before work may be covered up,
is eleven subprocess helpers wearing a different hat.

**The constraint that makes this safe, and it is not incidental.** The
attestation half must keep working when the vendor does not. So the inspector
has verbs that need no model and no key at all - `tally` today, `attest` when it
moves - and only `explain` reaches a vendor. Merging the gate into the inspector
must not make the gate depend on anything metered, or a merge would wait on a
quota. This is the same rule as "the vendor must never become a merge
dependency", one layer down: it now constrains the program's shape rather than
just the workflow's.

**What does not change.** No machine may resolve a conversation, including this
one - the inspector writes, a human resolves. The conversation stays keyed to a
digest of the sensitive part of the diff. Nothing blocks on a state only a human
action can clear. Those are the same four constraints the section below lists,
and moving the code does not touch any of them.

**The identity question follows the code, not the other way round.** Once the
inspector owns the gate and `explain` gives it something to say that a reviewer
did not already know, it wants its own App - scoped to `pull-requests: write`
and nothing else - so its actions are attributable and its permissions are its
own. Until then it posts as `github-actions[bot]`, which is what it does today.

### The inspector is a role before it is an identity

The sensitive-path gate has been called the inspector since the naming was
settled - the party who signs off before work may be covered up. Today it is a
step inside a workflow, posting as `github-actions[bot]`, and the question
raised was whether it should become an agent of its own whose only job is to
seek out sensitive changes and flag them.

> **Superseded.** The trigger this decision names has since fired, and the
> decision above replaces it. Kept because the reasoning is still the reason,
> and because a decision that was reversed is worth as much as one that held.

**Chose:** keep it a role for now, and record the trigger that would make it an
identity.
**Because:** the separation that matters already exists. The party that writes
the code is `fensberg-claude[bot]`; the party that flags it is
`github-actions[bot]`; and the party that acknowledges it must be a human,
which `scripts/attestation` refuses to let any machine do. Three distinct
parties, enforced rather than agreed. A dedicated App would make that legible
and let its permissions be scoped to `pull-requests: write` and nothing else -
which is worth having, and is not worth a second private key and a second
credential to rotate while the gate has one job and does it.

**The trigger is a second job.** An inspector that only opens attestation
conversations is a workflow step with a nice name. An inspector that also
answers "what changed in this diff that a human would want to know about" -
reading the change rather than matching a path - is a different thing, needs a
model, needs spend bounded like every other metered vendor, and should carry
its own identity so its actions are attributable and its permissions are its
own.

That second job is what this epoch is for. Until it exists, giving the current
gate an identity is ceremony: it would change who the comment appears to come
from and nothing else.

**One thing to carry across when it does happen.** The rule the attestation
enforces is that no machine may close a conversation. An inspector agent must
be held to it too - the agent that raises a concern must never be able to
resolve it, or the whole gate becomes one party talking to itself. That is the
same reasoning that stops Claude approving its own pull requests, applied one
layer down.

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

### Two programs, split where the lever is

**Chose:** `scripts/clerk` and `scripts/inspector`, as separate modules.
**Rejected:** one program with four verbs.
**Because:** the two halves differ in the only way that matters here — whether
they can stop anything. The inspector sits in the merge path and its output is
attached to a gate. The clerk is an outside party with no lever at all. The
operator's own framing settled it: an external reviewer "has no business
stopping anything", and its value is "yeah I looked at it, these are some
things I would do different, k bye".

The first draft bundled them on the `gatehouse` precedent, which combines three
responsibilities into one program. That precedent does not apply: gatehouse's
three share a _subject_ — the perimeter of the estate — while these four share
only a _technique_, which is how a utility library ends up wearing a role's
name. And this repository already argues against it structurally, with no root
module and one module per program, so that one program's dependency is not
every program's. A defect in the clerk's issue-filing has no business shipping
inside the binary standing in front of a merge gate.

### A second opinion, not an audit

**Chose:** the clerk reads the code and writes **its own account** of what it
does, without ever seeing our prose.
**Rejected:** giving it the claim and the code and asking where they disagree.
**Because:** a contradiction-hunter is an accusation engine, and every
accusation engine has false positives that cost real time to dismiss. This
record already rejected persona reviewers for exactly that reason — wrong
findings are expensive here specifically, because the operator relies on the
lanes rather than adjudicating a code claim from a diff.

Inverting it removes the failure mode structurally rather than filtering it. A
stranger's description of what the code does cannot be "wrong" in the corrosive
way a contradiction claim can, because it is not asserting that anything is
broken. Where it misreads the code, the misreading _is_ the finding — a
no-context reader getting it wrong is a signal about the code or its naming,
which is most of what the handover audit exists for anyway.

It also collapses two roles into one operation. The inspector's per-file
explanation already is this, pointed at a diff; the drift account is the same
thing pointed at a directory. One engine, two callers.

Anchoring is why the ordering matters. Hand a model the claim and the code
together and it will find agreement, because the claim primes it to look for
agreement. The clerk never sees the claim.

### Handover, not forkability, and not export

**Chose:** the clerk's verb is `handover`.
**Because:** `tests/go/repo/forkable_test.go` checks a _property_ by pattern —
no real names in the tree — and names it accurately. The clerk's job is the
_reading_: can the next party run this without us. The construction word for
that is a handover, the pack of drawings, manuals and as-builts given to
whoever was not there. "Export" was considered and reads like a data-export
feature. The building code's test keeps its name; renaming an established
component is its own piece of work.

### The clerk posts a COMMENT review, and permission cannot express why

**Chose:** the review event is hardcoded to `COMMENT`, with a building-code
test that fails if `APPROVE` or `REQUEST_CHANGES` appears in the clerk's review
path.
**Because:** the obvious instinct — a defanged approval — is a trap. **An
approval from a properly configured GitHub App counts toward branch
protection.** It is the standard trick for auto-approving Dependabot. Only
`github-actions[bot]` approvals are ignored, and building on "GitHub happens to
ignore this identity" is exactly the kind of load-bearing implicit behaviour
this estate has been burned by. `REQUEST_CHANGES` is worse: it blocks the merge
until a human dismisses it, handing the outside party both a lever and a
deadlock in one call.

The second instinct was also wrong, and is worth recording because it looks
right. A pull request is an issue, so `issues: write` ought to permit a comment
on one while making approval impossible. **It does not.** GitHub does not split
them: commenting on a pull request and reviewing one both sit under the Pull
requests permission. There is no grant that says "may comment, may not
approve", so the guarantee has to be code plus a test plus a repository rule,
not a permission wall.

The repository rule is the backstop and is now on: **Require review from Code
Owners**. With `CODEOWNERS` naming one human, no App approval can satisfy
`main` regardless of what any program does.

### The vendor must never become a merge dependency

**Chose:** when the model is unreachable, rate-limited or unkeyed, the
attestation conversation still opens, carrying today's static reason from
`.github/sensitive-paths`.
**Because:** the inspector improves a gate that blocks merging. A metered
third party sitting between the operator and a merge is precisely the deadlock
`sensitive-paths.yml` already warns about in its own header, and it would be a
self-inflicted one. The explanation is an enhancement to the gate, never a
precondition of it.

This is also what makes a free-tier rate limit a non-event: a 429 is
indistinguishable from unreachable, and both degrade the same way.

### Per-file conversations, and the defect they repair

**Chose:** one attestation conversation per sensitive file, each carrying a
plain-language explanation of what that file's change does, on pull requests
targeting `main`.
**Because:** this was asked for as readability and turns out to fix a real
defect. Today `sensitive-paths.yml` takes **one** digest across every sensitive
file and anchors it to `paths[0]`, so touching any one sensitive file
invalidates the acknowledgement already given for all the others. Per-file
digests make each acknowledgement stand or fall on its own file.

The rule listing then disappears on its own. It exists only because one comment
had to account for files it was not attached to — which this record already
identified as an anchor's job being done by prose.

Measured before scoping it: over the last twenty merge commits on `main`, nine
of ten touched at least one sensitive path, between one and six files each. So
this is a handful of conversations per pull request, not a wall. The digest
doubles as the cache key, so a push that changes nothing sensitive regenerates
nothing.

Scoping to `main`-targeting pull requests carries a security argument as well
as a cost one: a `pull_request` run from a fork receives no secrets, so a key
in this workflow would either leak into fork-triggered runs or make the gate
fail on them. In-repo branches only sidesteps that. It is narrower than it
sounds in the other direction — most pull requests currently target `main`.

### The free tier, and the rule that makes it free of cost as well as price

**Chose:** two Google accounts, two projects, neither with a billing account
attached; two keys at `op://homelab/source-control/{clerk,inspector}-bot/llm_key`.
**Because:** the operator's constraint was "either free or it isn't happening",
and free here is the better design rather than a concession. With no billing
account the estate's rule about bounding metered spend becomes structurally
true instead of enforced by `timeout-minutes` and a concurrency group — the
worst case is a 429, not an invoice, which matters when a vendor elsewhere
already holds the card.

Rate limits are **per project**, not per key, so two keys in one project would
let a clerk sweep silently eat the inspector's day on exactly the pull requests
where the blurb was wanted. Separate projects mean the roles cannot starve each
other; budgets are further per-model within a project, so the clerk's sweep
cannot eat its own review budget either.

The price of free is that content is used to improve Google's products, where
a paid tier's is not. Here that costs nothing **because everything sent is
already world-readable** — this is a public repository, and the estate keeps
secrets out of git by construction with a test enforcing it. Same shape as the
state-encryption argument: the material is worthless to whoever obtains it.

**That holds only while the input is restricted to what is already public**, so
it is building code rather than a caution. Both programs read from git and
nothing else. The moment either reads a rendered config, a run log, `talosctl`
output or anything off the Sterilize path, the data term becomes real. A test
enforces it, because the whole "free costs us nothing" argument rests on it.

### Under consideration: code scanning as the output surface

Not decided. SARIF uploaded to code scanning renders findings inline with a
dismiss control and three structured reasons, and dismissing resolves the
conversation. Three things recommend it for the clerk: dismissal reasons turn
this record's acceptance test from a judgement call into a count, because
_false positive_ versus _won't fix_ is exactly the split between "the clerk was
wrong" and "the clerk was right and we chose not to act"; alerts close
themselves when a finding stops appearing and reopen by fingerprint if it
returns, where an issue must be closed by hand; and the upload uses the
workflow's own token with `security-events: write`, attributed to the tool name
in the SARIF, so the clerk App keeps its narrow grant.

Two costs. It puts prose-drift findings in the security tab beside CodeQL,
Semgrep and Trivy — distinguishable by tool name and severity, but it dilutes a
surface that currently means "security". And code scanning results must stay
**out** of `main`'s required checks, or the outside party quietly acquires the
lever this whole design removes. They are not in there today.

Not for the inspector: its conversation is digest-keyed and deliberately
refuses machine resolution, which dismissal semantics would collide with.

## Acceptance tests

1. **The planner finds a trigger that has genuinely fired**, and the issue it
   opens is one somebody acts on rather than closes as noise.
2. **The clerk's handover audit finds a failure that
   `tests/go/repo/forkable_test.go` cannot express** — a semantic assumption
   rather than a literal name.

If either role cannot pass its test, it is decorative and comes out. A
mechanism whose output nobody acts on is worse than no mechanism, because it
looks like coverage.

## Gotchas

- **`ListModels` reports the catalogue, not the entitlement.** `gemini-2.5-pro`
  and `gemini-3.1-pro-preview` are both returned by the free-tier key, and both
  show 0 RPM / 0 TPM / 0 RPD on the account's rate-limit dashboard. Only a
  `generateContent` call settles whether a model is actually served. Pinning
  from the model list alone produces something that 429s on first use.
- **Free-tier budgets are not uniform across a generation.** Measured on the
  account, 2026-09-03: every full Flash model — 2.5, 3, 3.5, 3.6, 3.7, 3.8 — is
  capped at **20 requests/day** at 5 RPM. The Lite models `3.1-flash-lite` and
  `3.5-flash-lite` get **500/day** at 15 RPM. Both bands share 250K TPM. That
  constraint maps onto the roles rather than fighting them: the inspector sits
  in the merge path and takes the plentiful Lite model, the clerk's sweep has no
  deadline and can afford the scarcer, stronger one across days.
- **`gemini-3.8-flash` spends thinking tokens by default.** Six prompt tokens
  and one output token cost 88 thinking tokens on a "reply with one word"
  probe; `3.5-flash-lite` used seven tokens total with no thoughts. Chunk sizing
  has to budget thinking, not just input and output.
- **`serviceTier: standard` in a response is the latency tier, not the billing
  tier.** What actually guarantees free is that the project has no billing
  account attached.
- **An epoch branch cannot be deleted, force-updated, or moved by a ref write.**
  Its ruleset carries `deletion`, `non_fast_forward` and `pull_request`
  together, so a pull request into it is the only mechanism that exists. That
  makes a stale epoch branch expensive: cutting one fresh from `main` costs a
  single ref creation, and repairing one afterwards costs a pull request whose
  head is the default branch — a degenerate shape that Super-Linter's commit
  range computation does not handle. Cut the branch when the epoch starts, not
  before.

## Outcome

In progress.

## Deferred

- **Anything on small pull requests.** Trigger: the epoch pull request review
  proving useful enough to be worth the cost more often.
