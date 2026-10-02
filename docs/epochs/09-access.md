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
