# homelab

Infrastructure-as-code for a self-hosted homelab: Proxmox hypervisor -> Talos
Linux control plane -> Flux-managed workloads. Provisioned with OpenTofu and
Ansible, secrets brokered by 1Password.

## How this project is organized

Work happens in **epochs** — bounded phases with their own branch, PR, and
written record. Before starting or resuming work, read
[`docs/epochs/README.md`](docs/epochs/README.md) for the current state of the
world and links to every prior epoch's decisions.

The three tiers below are also the epoch boundaries:

| Tier        | Path            | Executed by                         |
| ----------- | --------------- | ----------------------------------- |
| Ignition    | `management/`   | Locally, by a human (the button)    |
| Abstraction | `modules/`      | Consumed by the tiers below         |
| Workload    | `environments/` | GitHub Actions + Flux               |

## Invariants

These hold across all epochs. Changing one is an epoch-level decision that
belongs in an epoch record.

- **No secret ever lands in git.** Secrets live in the 1Password `homelab`
  vault and are materialized at runtime by `op inject` into gitignored files.
  Everything rendered is wiped by the Sterilize phase.
- **No ClickOps, with one honest floor.** Anything a human would otherwise
  click — Proxmox SDN, overlay route approval, storage buckets — is codified.
  The floor is that credentials issued by third-party consoles (source control
  token, overlay OAuth client, object storage tokens) cannot themselves be
  automated, and the tailnet policy is set up once per tailnet rather than per
  deployment. See `docs/tailnet-setup.md`. Everything past that floor is code.
- **Ignition is deliberately local-only.** `management/` bootstraps the
  cluster that later runs CI-driven deploys, so it cannot depend on that
  cluster. Do not move it into GitHub Actions.
- **Declare the vendor, and assert it.** Each vendor-locked concern carries a
  `provider` field that `registry.tf` checks against what the code implements,
  so pointing the vault at another vendor's credentials fails the plan instead
  of failing opaquely at an API. Concerns that are genuinely portable, like
  source control over plain git, carry none.
- **Name things by function, never by vendor.** Config keys, 1Password paths,
  and file names describe what a thing does; the vendor lives in the value or
  in a file header. `source_control.token`, not `git.github_pat_reference`.
  The one place this cannot reach is Terraform resource names —
  `tailscale_tailnet_key` is irreducibly vendor-specific — so the abstraction lives at the config and
  secrets layer, which is what keeps a vendor swap to a single `.tf` file.
- **State migrates local -> Postgres, and is backed up twice.** The first
  apply runs on local state; once the cluster hosts Postgres, state moves
  there and the local copy is destroyed. CloudNativePG streams WAL and base
  backups to object storage for point-in-time recovery, and the Backup phase
  writes a standalone age-encrypted state dump alongside them. Both are
  needed: the database backups restore into a running cluster, but rebuilding
  that cluster needs the state held in the database. The standalone dump is
  what breaks that circle after a total loss.
- **The workspace is sterilized on every exit.** Success or failure, no
  secrets and no state are left on the workstation. On failure the run
  destroys infrastructure *before* wiping state, so nothing is orphaned.
- **Pin everything.** Actions to commit SHAs, providers to `~>` ranges, Talos
  to an exact version plus Factory schematic ID.

## The button

One entrypoint, run from Windows PowerShell:

```powershell
.\scripts\Install-Dependencies.ps1              # once, elevated
.\scripts\Start-Homelab.ps1 -Site chicago       # every time after
```

`-Site` selects an entry in `config/sites.json`.
Addressing, hypervisor placement and the cluster name all follow from it, so
adding a hypervisor is appending to that site's `nodes` array and adding a site
is adding a key. Nothing about the network is hardcoded, because two sites
advertising the same subnet onto one tailnet collide in a way that looks like a
broken network rather than a config mistake.

Topology is split on purpose. Structure - each site's octet and control-plane
count - is committed, because the octet is the most collision-prone value in
the system and needs review and automated checking. The hypervisors, their
addresses and their credentials live in the fleet document in the vault,
because for an MSP that is reconnaissance material.
`management/cluster/registry.tf` asserts the invariants at plan time: octets
unique, octets in range, registry and fleet agreeing, and each vendor-locked
concern declaring the vendor the code actually implements.

Phases run in order and can be run individually with `-Phase`, or resumed
with `-From`:

| # | Phase      | What it does                                          |
|---|------------|-------------------------------------------------------|
| 1 | Render     | `op inject` secrets into gitignored files             |
| 2 | Overlay    | Mint a tagged auth key to join the overlay network    |
| 3 | Hypervisor | Ansible: Proxmox repos, overlay network, RBAC, SDN    |
| 4 | Verify     | Prove the network path works before spending time     |
| 5 | Compute    | Create VMs, poll each node's Talos API                |
| 6 | Cluster    | Talos config, etcd bootstrap, Flux install            |
| 7 | Migrate    | Move state local -> cluster Postgres                  |
| 8 | Backup     | age-encrypt state, upload to object storage           |
| 9 | Sterilize  | Wipe every secret and the local state file            |

Ansible has no supported Windows control node, so phase 3 runs inside WSL2.
The script handles the hop; you do not type anything different. PowerShell is
a stopgap — the intended end state is to fold these phases into Ansible so the
entrypoint is cross-platform and there is no shell-specific orchestration.

## What lives where

| Path | Holds |
|------|-------|
| `management/hypervisor/` | Ansible: bare-metal Proxmox preparation |
| `management/cluster/` | OpenTofu: VMs, Talos, overlay network, storage, Flux |
| `clusters/management/` | Flux-reconciled manifests for this cluster |
| `config/management.tpl.json` | The secret contract, rendered by `op inject` |
| `config/sites.json` | Site registry: octet and control-plane count, in git so it can be reviewed and asserted |
| `config/fleet.example.json` | Shape of the fleet document; the real one is in the vault |

OpenTofu creates only what Flux cannot — namespaces and secrets. The operator
and the database itself are declared in `clusters/management/` and reconciled
by Flux, in two layers: `infra-controllers` installs CRDs, `infra-configs`
depends on it and uses them.

## CI

- `pr-validation.yml` — TruffleHog, Super-Linter, Semgrep, Trivy, Scorecard.
- `deploy-infrastructure.yml` — applies OpenTofu on a self-hosted runner.
  Path-filtered to `environments/**` and `modules/**`, so Ignition changes
  never trigger it. That is intentional.

## Conventions

- Branch per epoch: `epoch/<nn>-<slug>`. One PR per epoch into `main`.
- Close an epoch by filling in its record in `docs/epochs/` **before** merging.
- `pre-commit` is configured; run it before pushing.
