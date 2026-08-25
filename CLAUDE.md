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

| Tier        | Path            | Executed by                      |
| ----------- | --------------- | -------------------------------- |
| Ignition    | `management/`   | Locally, by a human (the button) |
| Abstraction | `modules/`      | Consumed by the tiers below      |
| Workload    | `environments/` | GitHub Actions + Flux            |

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
- **Declare the vendor three times, and make them agree.** The code implements
  one vendor per concern; the config declares one in `provider`, where it is
  reviewable in git; and the 1Password item attests one in `vault_provider`,
  travelling with the credentials themselves. `registry.tf` asserts all three
  match. Checking only the first two compares files that always change in the
  same commit, and cannot see the failure that matters - a vault item whose
  contents were swapped for another vendor's credentials. A shape check on the
  access key catches the careless version of that, where nobody updated any
  declaration at all. Portable concerns, like source control over plain git,
  carry none of this.
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
  destroys infrastructure _before_ wiping state, so nothing is orphaned.
- **Pin everything.** Actions to commit SHAs, providers to `~>` ranges, Talos
  to an exact version plus Factory schematic ID.

## The button

One entrypoint, run from Windows PowerShell:

```powershell
.\scripts\Install-Dependencies.ps1              # once, elevated
.\scripts\Start-Homelab.ps1 -Site site0        # every time after
```

**Windows is a stopgap.** `workstation/` provisions a Linux machine on the
hypervisor for day-to-day work. It is deliberately independent - no shared
config, no 1Password, and nothing in the cluster lifecycle can touch it, so a
failed ignition cannot take your development environment with it. See
[`workstation/README.md`](workstation/README.md).

`-Site` selects a key in the config's `sites` map. Each site declares its own
`octet`, which picks its `/16`, names the site and its VMs, and bands its VM
IDs - so `site10-cp-01` lives at `10.10.10.100`. Octets are asserted unique and
within 1-95 across every site, at plan time and again in the start button.

Scaling is three edits, each in one place:

| To add               | Do                                         |
| -------------------- | ------------------------------------------ |
| A control-plane node | Raise that site's `control_plane_count`    |
| A hypervisor         | Append to that site's `hypervisor.nodes[]` |
| A site               | Append to `sites[]`                        |

`control_plane_count` is a provisioning input, not an autoscaler. etcd quorum
is fixed at creation; see `docs/epochs/02-abstraction.md` for what actually
autoscales here and what cannot.

**What sits where.** Anything describing one estate lives inside the site -
`hypervisor`, `overlay_network`, `object_storage` and `state`, each with its
own asserted `provider`. Anything describing the whole fleet stays at the top:
`organization`, and `source_control`, because one repository drives every
cluster through Flux. Each site runs its own cluster with its own database, so
sharing state credentials across sites would mean compromising one reaches all.

Hostnames, credentials and even the site's human name are `op://` references,
so the file shows the shape of the estate without revealing what or where
anything is.

**Every `op://` reference in the template must resolve.** There is no way to
leave a placeholder site or node, so add the vault items first, then the
config entry.

## What lives where

| Path                         | Holds                                                      |
| ---------------------------- | ---------------------------------------------------------- |
| `management/hypervisor/`     | Ansible: bare-metal Proxmox preparation                    |
| `management/cluster/`        | OpenTofu: VMs, Talos, overlay network, storage, Flux       |
| `clusters/management/`       | Flux-reconciled manifests for this cluster                 |
| `config/management.tpl.json` | The one config: sites, topology and every secret reference |

OpenTofu creates only what Flux cannot — namespaces and secrets. The operator
and the database itself are declared in `clusters/management/` and reconciled
by Flux, in two layers: `infra-controllers` installs CRDs, `infra-configs`
depends on it and uses them.

## CI

- `pr-validation.yml` — TruffleHog, Super-Linter, Semgrep, Trivy, Scorecard.
  Super-Linter's settings live in `.github/super-linter.vars`, which `task
  lint` passes to the same image, so a local run reproduces CI exactly.
- `deploy-infrastructure.yml` — applies OpenTofu on a self-hosted runner.
  Path-filtered to `environments/**` and `modules/**`, so Ignition changes
  never trigger it. That is intentional.

## Conventions

- Branch per epoch: `epoch/<nn>-<slug>`. One PR per epoch into `main`.
- Close an epoch by filling in its record in `docs/epochs/` **before** merging.
- **Every linter has exactly one version in this project**, the one baked into
  the Super-Linter image pinned by SHA in `pr-validation.yml`. `task lint` runs
  it; `task fix` lets it correct formatting in place. Never add a linter to
  `pre-commit` that Super-Linter already runs - two copies of prettier is what
  put formatting errors on a pull request that `pre-commit` had just passed.
- `pre-commit` holds only hooks that shell out to nothing, so there is no
  second version of anything to drift. Run it before pushing.
