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
- **No ClickOps.** Anything a human would otherwise click — Proxmox SDN,
  Tailscale route approval, R2 buckets — is codified. If you find yourself
  reaching for a web console, that is a bug to be fixed in code.
- **Ignition is deliberately local-only.** `management/` bootstraps the
  cluster that later runs CI-driven deploys, so it cannot depend on that
  cluster. Do not move it into GitHub Actions.
- **State migrates local -> Postgres, and is backed up to R2.** The first
  apply runs on local state; once the cluster hosts Postgres, state moves
  there and the local copy is destroyed. An age-encrypted copy goes off-site
  to Cloudflare R2 for disaster recovery.
- **The workspace is sterilized on every exit.** Success or failure, no
  secrets and no state are left on the workstation. On failure the run
  destroys infrastructure *before* wiping state, so nothing is orphaned.
- **Pin everything.** Actions to commit SHAs, providers to `~>` ranges, Talos
  to an exact version plus Factory schematic ID.

## The button

One entrypoint, run from Windows PowerShell:

```powershell
.\scripts\Install-Dependencies.ps1     # once, elevated
.\scripts\Start-Homelab.ps1            # every time after
```

Phases run in order and can be run individually with `-Phase`, or resumed
with `-From`:

| # | Phase      | What it does                                        |
|---|------------|-----------------------------------------------------|
| 1 | Render     | `op inject` secrets into gitignored files           |
| 2 | Tailnet    | Apply tailnet policy (auto-approvers), mint auth key |
| 3 | Hypervisor | Ansible: Proxmox repos, Tailscale, RBAC, SDN         |
| 4 | Verify     | Prove the network path works before spending time    |
| 5 | Compute    | Create VMs, poll each node's Talos API               |
| 6 | Cluster    | Talos config, etcd bootstrap, Flux install           |
| 7 | Migrate    | Move state local -> cluster Postgres                 |
| 8 | Backup     | age-encrypt state, upload to Cloudflare R2           |
| 9 | Sterilize  | Wipe every secret and the local state file           |

Ansible has no supported Windows control node, so phase 3 runs inside WSL2.
The script handles the hop; you do not type anything different.

## CI

- `pr-validation.yml` — TruffleHog, Super-Linter, Semgrep, Trivy, Scorecard.
- `deploy-infrastructure.yml` — applies OpenTofu on a self-hosted runner.
  Path-filtered to `environments/**` and `modules/**`, so Ignition changes
  never trigger it. That is intentional.

## Conventions

- Branch per epoch: `epoch/<nn>-<slug>`. One PR per epoch into `main`.
- Close an epoch by filling in its record in `docs/epochs/` **before** merging.
- `pre-commit` is configured; run it before pushing.
