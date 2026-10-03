# Epoch 10 — Second Node

- **Tier / path:** `management/`, `scripts/contractor/`, `scripts/security/`
- **Branch:** none; each piece branches from `main`
- **PR:** #
- **Status:** Not started

## Goal

A site takes up a second Proxmox node with no manual step after a vault
entry. At the end, credentials go in the site's vault and the site admits the
server, joins it to the site's Proxmox cluster and gives it work, with nobody
having run anything.

Cleaved from [`05-node-lifecycle.md`](05-node-lifecycle.md) on 2026-10-03.
That epoch is about replacing the machines a site already has, and all of it
can be built and proved on one node. This half cannot: none of it can be
exercised until a second server exists, and a second node is a direction the
estate grows in, not something being bought now.

**Trigger: a second node is being bought.** This epoch is built before that
node is given work, and not before the decision to buy it.

## Scope

In scope:

- **Adopting a node from a vault entry**, as designed below.
- **A Proxmox cluster per site**, and the third quorum vote a two-member
  cluster needs.
- **Signed enrolment** and the checks Security runs on the key.
- **Machine classes (#566)**, because a second host with different hardware
  is what makes the size literals wrong.

Explicitly out of scope (and which epoch owns it instead):

- **Moving running machines safely** - cordon, drain, etcd member removal,
  replace, wait for health. Epoch 05 builds it and this epoch uses it.
- **A second site.** A site is declared in git and built by `build-site`;
  see [`02-abstraction.md`](02-abstraction.md).
- **Inbound mail.** The server that holds the third vote is also the likely
  home of the mail relay's public address, which is epoch 03's.

## Decisions

### Adding a node needs no manual step after the vault entry

Agreed 2026-10-02, while closing epoch 02, where this began as "adding a
node needs no commit". Not built. Credentials go in the site's vault and the
site takes up the capacity; nothing else is done by hand.

- **The nodes a site has are checked, never declared and never guessed.**
  The contractor asks the site which nodes are there and assigns work
  accordingly. One up is a site with one node; two up is a site with two.
  A node gets a default share of workers.
- **Machines may move when a node is adopted**, so the work is in making a
  move safe - cordon, drain, remove the etcd member, replace, wait for
  health - which is epoch 05's subject and has to exist first.
- **A Proxmox cluster per site**, not nodes standing alone. So the site's
  one API token covers a node the moment it joins, and the site's runner
  mints and stores nothing: its vault access stays read-only and no new
  vault is needed. The costs are Proxmox's own: a join needs root on a
  member that is already running, every member is root on every other, and
  two members are not a quorum when one is down. The third vote is the
  decision below.
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

**Open:** whether the adoption runs on GitHub's scheduler, which is late
more often than not, or on a timer inside the site.

### The third vote is a witness outside every site

Agreed in direction 2026-10-03. Nothing is bought or built.

**Chose:** a Proxmox QDevice on one small rented server at estate scope,
serving every site that has exactly two nodes. A site that reaches three
nodes has its own quorum and drops the witness, which Proxmox discourages
for an odd number of members anyway.
**Because:** it is what is done for a two-node site that cannot justify a
third server - one witness, held centrally, for many sites. It adds one
host for the estate, not one per site. The members dial out to it, so no
site opens an inbound port. And it holds no access to a cluster: a witness
that is compromised can withhold or misgrant its vote, which costs
availability and nothing else.
**Rejected:** a third full node per site, which is the better answer and
the one a site grows into, but is a whole server bought for a vote. A
witness on a small box at the site, which is one more device per site to
enrol, patch and secure, on the same power and network as the members it
votes for. No third vote at all - weighting one member or forcing the
expected votes down - which freezes the cluster when the weighted member is
lost and which Proxmox advises against.

Two things the design has to hold to when it is built:

- **The witness cannot run on a cluster it votes for**, so not a VM on a
  site's own nodes.
- **Its port is reachable only over the estate's private path.** The same
  server is the likely public address for inbound mail, and the vote must
  not be offered on that address.

**The cost that comes with it:** a site whose connection is down and which
then loses a node has a survivor without quorum, and a Proxmox member
without quorum cannot start or change a VM. A site at full strength is
unaffected by losing the witness.

## Outcome

## Deferred

- **Machine classes (#566).** VM sizes are literals in `compute.tf`: control
  planes 4 cores / 4 GiB, workers 6 / 10 GiB, disks 64 and 32 GB. They belong
  in named classes a site picks from per role, as cloud instance types are.
  Deferred until a second host with different hardware makes the literals
  wrong; found in epoch 02's abstraction review, 2026-09-27. Moved here from
  epoch 05 with the rest of the second node's work.

## Gotchas

- **Placement is a re-deal until epoch 05 retires it.** `vm_placement`
  recomputes `i % length(hypervisors)`, so adding a hypervisor reassigns
  the machines already running. Epoch 05 holds the item; this epoch cannot
  adopt a node while it stands.
