# Epoch 07 — Metered Egress

- **Tier / path:** `clusters/management/`, `scripts/`
- **Branch:** `epoch/07-metered-egress`
- **PR:** #
- **Status:** Not started
- **Closed:** <date>

## Goal

Make it impossible for this estate to spend money it did not mean to. Every call
to a metered vendor goes through one interface that knows the budget and refuses
before the call rather than reporting after it, and that interface is the only
thing holding the vendor's credentials.

At the end, a runaway loop anywhere in the estate produces a refusal and an
alert, not an invoice.

## Why a gateway rather than alerts

**Cloudflare has no hard spend cap on R2.** Notifications tell you after the
fact; nothing on the vendor side will refuse a call because the month has cost
too much. So a hard cap has to be self-imposed, and the only place that can
enforce one is a chokepoint every caller passes through. That is the whole
argument for building something rather than configuring something.

The second reason is credential isolation, and it may be the larger one. Today
the object-storage keys are handed to whatever needs them - the Backup phase,
the database operator. If the gateway is the only holder, then nothing else in
the estate can reach the vendor at all, and a compromise elsewhere yields no
ability to spend. That is the same reasoning as encrypting state so a leaked
copy is worthless.

## The three problems that decide whether this is real

### 1. The vendor's own usage numbers lag, so they cannot be the gate

R2 usage is reported through an analytics API that trails real time. A gateway
that asks "how much have we used?" before every call would be pacing itself
against a number that is minutes to an hour stale, and could overshoot badly
inside that window - which is exactly the runaway case it exists to stop.

So the gateway must **count its own calls locally**, and treat the vendor's
figures as reconciliation rather than as the budget. Its own counter is
immediate and authoritative for admission; the vendor's tells it whether its
counting has drifted.

### 2. The dominant caller is the hardest one to route

Workflows are the visible callers and the easy ones. The database operator is
not: it streams write-ahead logs to object storage continuously, through its own
S3 client, and it will out-call every workflow in this repository combined. A
gateway that covers the easy callers and not that one bounds almost nothing
while looking like it bounds everything.

Covering it means the gateway speaks S3 itself and the operator is pointed at it
as an endpoint - a proxy, not a helper library. That is materially more work
than a connector with a nice API, and it should be understood as the real scope
rather than discovered halfway.

### 3. It becomes a single point of failure on a durability path

Once backups go through it, the gateway being down means backups stop. That
turns a cost control into an availability risk on the one path this estate
cannot afford to lose, so the failure mode has to be a decision rather than an
accident:

- **Over budget:** refuse. That is the entire point.
- **Gateway unreachable, or its own budget state unreadable:** this is the hard
  one. Refusing protects the card and stops write-ahead-log archiving, which is
  the thing that makes point-in-time recovery possible. Allowing protects
  durability and reopens the hole.

The current preference is to fail **closed on cost** and **open on durability**:
refuse when the budget is known to be exhausted, allow when the budget is merely
unknown, and alarm loudly in the second case. A known-exhausted budget is the
scenario this is built for; an unknown one is a fault in the gateway, and
answering a fault in the safety mechanism by destroying backups is the wrong
trade. This should be revisited with real numbers.

## Scope

In scope:

- The gateway itself, holding the only object-storage credentials in the estate.
- Local, authoritative call and byte accounting, reconciled against the vendor.
- Pointing the Backup phase and the database operator at it.
- A budget in config, per site, reviewable in git like everything else.
- Alerting on refusal and on reconciliation drift (with epoch 04).

Explicitly out of scope (and why):

- **Cloudflare's control-plane APIs** used by OpenTofu for DNS and bucket
  creation. Those are called by a provider, not by our code, and are not billed
  per operation in a way that can run away. Proxying them would mean a custom
  provider for no benefit.
- Vendor-side spend notifications. Those stay, and should be configured now
  rather than waiting for this epoch - they are the backstop for anything that
  bypasses the gateway, including the gateway being wrong.

## Decisions

_To be filled in as the epoch runs._

## Outcome

_To be filled in at close._
