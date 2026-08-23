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
.\scripts\Start-Homelab.ps1 -SiteIndex 0        # every time after
```

`-SiteIndex` selects an entry in the config's `sites[]` array. The index is the
site's identity: it names the site, picks its `/16`, numbers its VMs and bands
its VM IDs. Two sites cannot share an octet because two array entries cannot
share an index, so that collision is not expressible rather than merely
checked for.

Scaling is three edits, each in one place:

| To add | Do |
|---|---|
| A control-plane node | Raise `control_plane_count` |
| A hypervisor | Append to that site's `hypervisor.nodes[]` |
| A site | Append to `sites[]` |

Hostnames and credentials are `op://` references, so the file shows the shape
of the estate without revealing what or where anything is.

## What lives where

| Path | Holds |
|------|-------|
| `management/hypervisor/` | Ansible: bare-metal Proxmox preparation |
| `management/cluster/` | OpenTofu: VMs, Talos, overlay network, storage, Flux |
| `clusters/management/` | Flux-reconciled manifests for this cluster |
| `config/management.tpl.json` | The one config: sites, topology and every secret reference |

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
