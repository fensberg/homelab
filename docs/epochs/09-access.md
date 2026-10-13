# Epoch 09 — Access

- **Tier / path:** `management/estate/`, `modules/infrastructure/address-plan/`
- **Branch:** none; each piece branches from `main`
- **PR:** —
- **Status:** Not started

## Goal

Least privilege between sites and into them. Today any enrolled member
reaches every route: the Zero Trust policy admits the member list, and every
tunnel route sits in the device profile's include list. At the end of this
epoch a person or a machine reaches exactly what it has been granted, by name,
and everything else is blocked at every point the traffic passes.

The operator's statement of it, 2026-09-27: "We don't need ANYONE / ANY
MACHINE at site0 accessing site1 - we need least privilege and only a specific
person or machine at site0 accessing site1. But IAM shouldn't be IN the
addresser - the addresser should just reference it and block what's not
allowed."

## Scope

In scope:

- **Grants.** An estate-level declaration of who may reach what. Principals
  are identities - people by their identity-provider identity, machines by
  site and workload - and destinations are the address plan's names, never
  addresses. Nothing is granted by default. It is IAM, so it is the estate's:
  the lawyer holds it, and the identities live in the vault as the member list
  does today.
- **Enforcement, compiled from grants and the address plan**, at each point
  traffic passes, default deny:
  - people: Cloudflare Zero Trust Gateway network policies by identity,
    replacing the member-wide route list;
  - machines across sites: WARP Connector at each site carrying site-to-site
    traffic, with Gateway network policies by source address - the address
    plan is what turns "this machine" into an address - and Access service
    tokens for machines calling an HTTP app;
  - inside each cluster: a Cilium policy denying egress to other sites'
    ranges except for the workloads granted it, so a mistake at one layer is
    not open access.
- **Private names.** `proxmox.<domain>` and the like resolve only on the
  tunnel, through Cloudflare Tunnel hostname routing, and never have a public
  record; the public site keeps an ordinary public record in the same domain.
  Real certificates for private names through DNS validation.

Explicitly out of scope:

- The address plan itself - epoch 02 builds it, and this epoch consumes it.
- DNS servers inside the estate, unless hostname routing proves not to cover
  a case; if it does, that is decided here with the evidence.

## Decisions

### Three parts, each ignorant of the others' details

**Chose:** the address plan says what exists and where; grants say who may
reach what, by name; enforcement is compiled from both and blocks everything
else.
**Because:** the operator's requirement that IAM not live in the addresser,
and because a name-based grant survives an address change that an
address-based rule would silently stop matching.
**Rejected:** grants written as addresses, and access rules inside the
address plan.

### Cloudflare Zero Trust for people and machines, on the free plan

**Chose:** Cloudflare as the IAM and enforcement point for both.
**Because:** checked against Cloudflare's own documentation on 2026-09-27,
the free plan includes 50 seats of Access and Gateway, tunnels including WARP
Connector, service tokens, Gateway network policies by identity, and Tunnel
hostname routing for private hostnames.
**To confirm first:** how connectors count against the 50 seats.
**Open:** if Cloudflare carries site-to-site traffic, the tailnet stops being
needed between sites - one supplier fewer. Weighed here, not assumed.

### A canonical per-site name, and aliases as data

**Chose:** every destination is `<service>.<site>.<domain>`, and a declared
alias such as `proxmox.<domain>` points at one site.
**Because:** the operator wants `proxmox.<domain>` to reach site0's dashboard
today, and site1 may be years away. An alias is one line of data: when site1
arrives it keeps pointing where it did until that line changes, and nobody has
to learn site names in the meantime.
**Guard:** the address plan refuses a private name or alias that collides
with a declared public one, so an internal service cannot shadow the public
site.

### The estate's settings at its source control are set from git (decided 2026-10-13)

Who may send work to the estate, and what that work may do, is decided by
settings of the organisation and the repository: who may open a pull
request, whose workflows run without being let, who is a collaborator,
which repositories and workflows may use the estate's runners, which
actions may run, what a workflow's token may do, and the rules on main.
Every one was made by hand and read by nothing. They became the estate's
outermost wall the day a pull request's jobs were to run on the estate's
own hardware ([08](08-agent-roles.md), #559). The operator, of the design
that came first, which held a hostile job once it was running: "This repo
should be a walled garden. You're focusing on the garden and not the
walls."

**Chose** to declare them in the estate's own root and set them from
there, **over** a check that reads them and stops everything when one is
wrong. The operator: "We don't need to check 1-6. We just set it." And
over a nightly comparison: "Just force this on every run. Why check when
constantly validate and override?" So they are applied on every merge to
main and on a timer between, from the estate's own runner and never from
a pull request's job, since what applies them can take every wall down.
An apply that had to change something says so: a setting found otherwise
was changed by somebody.

**Built so far:** the estate reads its organisation and repository with a
credential of its own (the lawyer's, in the estate vault), and sets
nothing. Each wall is declared next as it stands, so the first apply moves
nothing, and tightened after that one at a time.

**It carries the estate's converge with it.** Nothing converged the estate
when a change to it merged (#535); it is converged on the same merge and
the same timer. The estate's credentials are then used by a job on the
estate's runner where they had only been in the operator's shell, which
the operator agreed to knowingly.

### What is set at the source control outlives the estate (decided 2026-10-13)

The repository is where the estate is declared, and its walls stand whether
or not an estate does. So they are treated as the buckets are:

- **A demolish releases them.** Everything the estate set at its source
  control is taken out of the estate's record before the destroy, so the
  destroy cannot reach it. Before this, demolishing the estate would have
  taken the repository's walls down with it.
- **A build adopts them.** The rules on the repository's branches were made
  by hand before any of this. The lawyer asks the source control which
  stand, by name, with the estate's own App, and the estate takes those as
  its own. One that does not stand is made.
- **The adoption is written for one run.** The lawyer writes import blocks
  beside the root and removes them when the run ends. An import in git was
  the standard way and was not open: OpenTofu refuses an import under the
  mocked providers the estate's own tests run with. And an import by
  command changes the estate's record, which a plan must not. Written this
  way `plan-estate` shows what a converge would alter in what it adopts.
- **Nobody passes the required checks by.** One person could, always. A
  check that is broken is mended.

- **One party passes the review by:** the App that brings a pin or a
  delivery. It passes the review and not the checks.
- **A member of the organisation is given nothing but reading.** Making a
  repository or a site of pages no longer comes with membership.
- **The organisation's settings are always adopted and never made.** The
  provider's resource writes every field it has, and making it would have
  written the organisation's name and description empty.

One switch of the review rule has no word in the provider: an extra
approval for a change nobody is attributed with. It is on, set by hand, and
stays on while a plan shows no change to that rule.

### The estate's runners take only named workflows, from the default branch (decided 2026-10-13)

A runner of the estate's is a machine inside it. Which work may reach one
was whatever a workflow file asked for, held back by a guard in this
repository and by review. It is now also the platform's to refuse: the
runners' group admits the estate's own repository, and of it only the
workflows that converge a site and that test against one, each as it is on
the default branch. A workflow changed on another branch, or started by hand
from one, is given no runner.

The group existed and is adopted by name, as the rulesets are.

**Applying on a timer is deferred.** The settings at the source control are
to be applied on every merge; applying them again on a timer, and the alert
for an apply that had to change something, are put off (#535, #549). Until
the merge-time apply is built a converge of the estate is run by hand.

### The estate's Apps are declared, and made by one click (decided 2026-10-13, not built)

The estate acts at its source control as five parties, each an App or a
token: the agent that writes changes, the one that opens deliveries and
reverts, the one that reads a pull request and says what it found, the one
that registers runners, and now the lawyer's. What each may do was set in
a browser when it was made and is written nowhere. Asked how many there
were, the answer had to be worked out from which secrets the workflows
name.

The operator: "I'm starting to think the Apps themselves should be built
in code."

**What the vendor allows.** An App cannot be made by a call alone: an
owner confirms it in a browser. What is confirmed can be a file, a
manifest that says the App's name and exactly what it may do. And an App
can read what it was granted.

**Chose**, for when it is built:

- each App's manifest is in git, so what it may do is declared and
  reviewed like anything else;
- making one is the one click the vendor insists on, which is the floor
  this estate already accepts for a credential a vendor's console issues;
- each converge has every App read what it was granted and compares that
  with its manifest, and stops on a difference. This is checked and not
  set, because it cannot be set from outside: here a check is all there
  is.

**Found on the way, and to be cleared:** two client secrets on the
agent's App that nothing has ever used, since it proves itself with a key;
and two personal tokens of the operator's, one never used.

### The workstation has a tunnel of its own, locked on the workstation

Built 2026-10-02, ahead of the rest of this epoch, because the operator was
about to be away for a week with the laptop off the overlay.

A site's tunnel carries that site's applications and its connector runs in
that site's cluster. The workstation is no site's and must be reachable on
the day a cluster is not, so it has its own: the estate makes one tunnel
with one route (`management/estate/workstation.tf`), and the connector runs
on the workstation as a service (`workstation/tunnel.sh`).

- **The address is the workstation's own**, written in the estate's config
  and held on the workstation's loopback. Not the address the house's
  router hands out, which moves with a lease, and in a range no other
  network hands out, so nothing shadows it from a hotel.
- **What the connector may open is decided on the workstation, not at the
  vendor.** It runs as a user of its own, and a firewall rule holds that
  user to this machine's SSH port, the resolver and the vendor's edge. A
  route added to the tunnel by mistake, or by somebody who took the
  account, reaches nothing else in the house. The rule is a unit the
  connector is bound to: no rule, no connector.
- **It proves itself.** Installing and checking both try, as the
  connector's user, the SSH port (which must answer) and the gateway and
  this machine's other addresses (which must not, and must answer for
  somebody not held to the rule, or the proof is of nothing).
- **Its token is granted to nobody.** The install asks the account for it
  once, with the token the estate is converged with, and writes it only into
  a file the connector's group can read. A grant would have meant a new
  vault, and a new service account to reach it.
- **Getting in takes an enrolled device and an SSH key.** The tunnel adds a
  path, not a credential.

Not done: the provisioning playbook does not install this, so a rebuilt
workstation needs `workstation/tunnel.sh install` run once.

## Outcome

## Deferred

## Gotchas

- **The domain is a vault value.** The estate's domain names the
  organization, and the repository is forkable: it appears nowhere in code,
  fixtures or documentation, and the address plan reads it from the vault.
