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

One entrypoint, a Go program, run from the Linux workstation:

```sh
./scripts/install-dependencies.sh   # once
task start SITE=site0               # builds ignite and prints the command to run it
./scripts/ignite/ignite -site site0 # the actual run - always run this directly, never through task
```

**`task start` deliberately does not run ignite itself.** `task` intercepts
Ctrl-C for its own purposes but does not proxy the signal to the process it's
supervising - a confirmed, currently-open upstream limitation
(`go-task/task#1408`). Ignite's own destroy-then-sterilize cleanup on
interrupt only runs if something actually delivers it the signal, so the
real ignition run has to be invoked directly. Every other `task`-wrapped
ignite phase (`render-secrets`, `verify`, `configure-hypervisor`,
`backup-state`, `clean-secrets`) stays safe to wrap regardless, because none
of them can reach the Compute phase - an interrupted one leaves stale
secrets at worst, recoverable with `task clean-secrets`, never an orphaned
VM.

**`workstation/` provisions the Linux machine this runs from.** It is
deliberately independent from `management/` - no shared config, no
1Password, and nothing in the cluster lifecycle can touch it, so a failed
ignition cannot take your development environment with it. See
[`workstation/README.md`](workstation/README.md). Ignition was originally a
PowerShell entrypoint run from Windows, with Ansible hopping into WSL2
because it has no supported Windows control node; once `workstation/` made a
Linux dev machine the norm rather than a stopgap, that whole layer became
unnecessary and the entrypoint moved to Go - see the epoch record for why Go.

`-site` selects a key in the config's `sites` map. Each site declares its own
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
| `tests/`                     | Everything above the unit tier — see `tests/README.md`     |

OpenTofu creates only what Flux cannot — namespaces and secrets. The operator
and the database itself are declared in `clusters/management/` and reconciled
by Flux, in two layers: `infra-controllers` installs CRDs, `infra-configs`
depends on it and uses them.

## CI

- `pr-validation.yml` — eight lanes, all running in parallel, Format
  included. Formatting is enforced locally first (the git hook
  `./scripts/install-dependencies.sh` wires up via `pre-commit install`) -
  shift left, catch it in seconds on the machine that wrote it. **Format**'s
  CI lane, running the same `.pre-commit-config.yaml` as that hook, exists as
  the backstop for whoever's local hook is missing or bypassed: a fresh clone
  that skipped setup, or a bot/outside PR that never touches a git hook at
  all. It does not gate the other lanes - a formatting slip no longer delays
  or blocks the lanes that actually check correctness and security, and every
  lane's result lands for every PR at roughly the same time, not staggered
  behind however long Format took. **Shell Lint** runs ShellCheck directly,
  pulled out of Super-Linter for the same one-owner-per-check reason as Go
  and Trivy below. **Validate** proves the code resolves: `tofu validate`
  against a placeholder config, and `kustomize build` piped through
  `kubeconform` with the Flux substitutions applied - Go vetting/building
  lives here too, for the same reason. **Test** is the behaviour half of Validate: Go unit and
  contract tests, `tofu test` against the fixture corpus, and the
  JavaScript/TypeScript tier. Everything in it is hermetic, which is what
  lets a fork's pull request run it in full without reaching a credential.
  **Analyze** is Super-Linter, for
  everything not already owned by a dedicated lane. **Semgrep**, **Trivy**
  and **Secrets** are the security lanes, and overlap with each other and
  with Analyze on purpose - none of them comes out.
- `codeql.yml` — CodeQL on `actions`, the only language here it supports.
  Workflows are the part of this repository that runs with a token, so that is
  where a finding matters. Moved off GitHub's default setup so it is pinned and
  reviewable; the two are mutually exclusive, so default setup must stay off.
- `scorecard.yml` — repository posture, weekly and on merges to `main`. It
  grades the repository rather than the diff, so a pull request cannot change
  its answer.
- **Egress is deny-by-default.** Every job pins `harden-runner` to
  `egress-policy: block` with an explicit allowlist, so a compromised action or
  linter cannot exfiltrate quietly. The one exception is the TruffleHog lane,
  which stays on `audit` and says why in a comment: verification works by
  calling the API of whichever vendor issued a leaked key, and an allowlist
  would silently downgrade verified findings to unverified rather than fail.
- `integration-tests.yml` — the test tiers that need a real estate, on the
  self-hosted runner: nightly, plus manual dispatch. Not reachable from a
  pull request, deliberately. The nightly run exists mostly for one
  assertion no pull request can make - a `tofu plan` against real state,
  which is the only thing here that can notice somebody changed a VM in the
  Proxmox web UI. The destructive e2e tier is absent from CI entirely and is
  run by hand; see `tests/README.md`.
- `deploy-infrastructure.yml` — applies OpenTofu on a self-hosted runner.
  Path-filtered to `environments/**` and `modules/**`, so Ignition changes
  never trigger it. That is intentional.

## Testing

Tests come before the code they test. `tests/README.md` is the map; the two
things worth knowing without reading it:

**Five tiers, one line that matters.** unit and contract are hermetic - no
1Password, no hypervisor, no credentials - so a pull request runs them in
full, including one from a fork. integration, api and e2e need a real estate
and never run on a pull request.

**The config contract is checked, not assumed.** `registry.tf` and
`internal/config/config.go` implement the same invariants twice, so a bad
config is refused whether it arrives through the start button or a bare
`tofu plan`. `management/cluster/tests/fixtures/manifest.json` is the single
corpus both sides are run against, and the contract tests fail if a case
exists on one side and not the other. Adding an invariant means adding it in
both places and adding a case to that manifest.

Coverage is a ratchet, not a threshold: `tests/coverage-baseline.json` is a
floor a pull request may not drop below and is free to leave alone.

## Conventions

- Branch per epoch: `epoch/<nn>-<slug>`. One PR per epoch into `main`.
- Close an epoch by filling in its record in `docs/epochs/` **before** merging.
- **Each check has exactly one owner.** Formatting belongs to `pre-commit`,
  pinned to exact versions; CI runs that same file rather than its own copy of
  the same tools. Analysis belongs to Super-Linter, pinned by image SHA, with
  every formatter it would duplicate switched off in
  `.github/super-linter.vars`. Nothing is configured in both places - two
  copies of prettier, on two versions, is what put formatting errors on a pull
  request that `pre-commit` had just passed.
- Four verbs, fastest first: `task fix` formats (seconds, no Docker),
  `task validate` proves the OpenTofu and manifests resolve, `task test`
  proves they behave, `task lint` runs the slow analysis image. The first
  three run on every push (the `pre-push` hook wires up `validate` and
  `test`); the fourth is worth running before opening a pull request.
  `validate` and `test` are separate on purpose - "does it resolve" and
  "does it do the right thing" are different questions, and one owner per
  check is the rule everywhere else here too.
- **Formatting is enforced locally first, shifted as far left as this repo
  can reach.** `./scripts/install-dependencies.sh` wires `pre-commit` into the
  `pre-commit` git hook, so a formatting mistake is caught on the machine
  that made it - in seconds, not minutes later in CI. (Not also `pre-push`:
  in this project's actual workflow a commit is pushed within seconds of
  being made, so a second hook there would just re-check the identical diff
  pre-commit had already checked moments earlier - no real coverage gained
  for the cost of running it twice.) Nothing client-side is ever truly
  unbypassable (`--no-verify` exists, and GitHub.com has no server-side
  pre-receive hook to close that gap), so Format's CI lane isn't redundant
  with the local hook - it is what actually
  catches a bypassed hook, a fresh clone that skipped setup, or a bot/outside
  PR that never runs a local hook at all. That lane runs in parallel with
  everything else, not gating it, so a formatting slip on someone else's PR
  never delays or blocks the lanes that check correctness and security.
