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

## Node, site, estate

Three words that get used precisely here, smallest to largest. They are not
interchangeable, and the boundaries between them are where isolation decisions
get made.

| Term       | Is                                                                                                                   | Shares                                              |
| ---------- | -------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------- |
| **Node**   | One Proxmox machine                                                                                                  | Everything with its site                            |
| **Site**   | One cluster, one `/16` picked by its `octet`, its own control plane and its own database                             | The fleet plane with every other site in the estate |
| **Estate** | Every repository under one GitHub organization, and the vault, break-glass key and object storage account they share | Nothing with another estate                         |

**A site is an isolation boundary; an estate is a blast radius.** Sites do not
share a database password, precisely so that compromising one does not reach
the others - the backup recipient is the exception because it is a public key
and sharing it costs nothing. But every site in an estate does share the fleet
plane: one repository drives them all through Flux, one vault holds their
secrets, one break-glass key decrypts their backups.

**So a second estate is not a bigger version of a second site.** It is a second
copy of the whole apparatus - its own organization, its own repositories, its
own vault, its own break-glass key - and it is what you want when the isolation
has to be total rather than merely per-cluster. Adding a client is normally a
new _site_, which is the cheaper model and the one
[`02-abstraction.md`](docs/epochs/02-abstraction.md) is designed around.

Anything scoped to the estate rather than to one site is **organization-scoped**
by construction, because the organization is what bounds the estate. That is
the rule that decides where a shared credential belongs: `organization`,
`source_control` and `state_backup` sit at the top of the config for the same
reason the runner's GitHub App is installed on the organization rather than on
a repository.

## Tiers, and the one cluster they share

The three tiers are directories and epoch boundaries. They are **not**
deployment stages, and conflating the two is a mistake this repository has
already made once in CI.

| Tier        | Path            | What it is                        | Staged? |
| ----------- | --------------- | --------------------------------- | ------- |
| Ignition    | `management/`   | The cluster itself - the platform | **No**  |
| Abstraction | `modules/`      | Reusable definitions              | n/a     |
| Workload    | `environments/` | What runs on the platform         | **Yes** |

**`site0` is one cluster, and it hosts both staging and production
workloads.** It is a development platform being built so that real production
workloads can be deployed onto it. So `environments/staging/` and
`environments/production/` are two overlays landing on the same hardware,
separated by namespace and configuration rather than by cluster.

**The management tier therefore has no staging or production form.** There is
one platform. A converge either changes it or does not; there is no staging
copy of it to change first. Anything that labels a management operation
"staging" or "production" is borrowing a word from the tier above and will be
wrong the moment a production workload lands on the same cluster - a job
labelled `staging` would be the one restarting a control plane that production
depends on.

### The four GitHub environments

Each gates one thing, and none overlaps another:

| Environment   | Gates                                       | Triggered by                 |
| ------------- | ------------------------------------------- | ---------------------------- |
| `management`  | the platform itself - `contractor converge` | merge touching `management/` |
| `staging`     | workload deploys                            | merge to `main`              |
| `production`  | workload deploys                            | tag `v*`                     |
| `integration` | the test tiers that need a real estate      | nightly, or dispatch         |

`management` is a fourth environment rather than a reuse of `staging`
deliberately. Reusing one would look leaner and would mislabel the job with the
largest blast radius in the repository. The secret it holds is scoped to match:
read and write on the `homelab` vault only, because that is the only vault the
platform's own secrets live in.

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
  deployment. The age backup keypair sits on that floor for a different
  reason: it _could_ be automated, and must not be. Its whole job is to
  survive this automation being compromised, so a key this program generated
  and stored somewhere this program can read would foreclose the property in
  the same breath as creating it. Generated once for the estate with
  `age-keygen` — one break-glass key, not one per site — and never referenced
  by OpenTofu, which `tests/go/repo` enforces.

  Everything below that floor is generated: the state database password is
  created by break-ground and written to 1Password, because the rule that decides is
  where a secret ends up. A secret that becomes a resource attribute is
  written into OpenTofu state, so a leaked state file yields a live
  credential; those are ours to generate. A secret that only configures a
  provider never reaches state at all. See `docs/tailnet-setup.md`. Everything past that floor is code.

- **Ignition is local-only; convergence is not.** These are two different
  operations and conflating them is what made the button a first-run tool
  without anyone noticing.

  _Ignition_ builds the cluster that later runs CI-driven deploys, so it
  cannot depend on that cluster. It stays local. Do not move it into GitHub
  Actions.

  _Convergence_ applies a config change to an estate that already exists. The
  circular dependency ignition has does not apply - the cluster is already
  there, holding the state - so a converge may run wherever it can reach the
  estate, including on the self-hosted runner inside it. `contractor converge`
  attaches to the state in Postgres instead of starting from an empty
  workspace, never runs Migrate, and never destroys on failure.

  The distinction is load-bearing rather than pedantic. After a successful
  ignition, Sterilize removes both the local state file and `backend_pg.tf`,
  so a second `break-ground` run starts empty, plans to create every VM again, and
  would copy that empty state over the real one with `-force-copy`. Changing
  `management/` had no supported path at all until converge existed.

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
  in a file header. `source_control.repo_url`, not `git.github_repo_url`.
  The one place this cannot reach is Terraform resource names —
  `tailscale_tailnet_key` is irreducibly vendor-specific — so the abstraction lives at the config and
  secrets layer. That keeps the _OpenTofu_ half of a vendor swap small: 60 lines
  in `overlay-network.tf` plus a provider block. It does **not** make the swap
  small overall — `hypervisor-prep.yml` carries ~50 Tailscale-specific
  references (repo key, install, `tailscale up`, route advertisement,
  re-auth handling), and that is where a real overlay migration would be
  spent. See `docs/epochs/02-abstraction.md`.
- **Fail closed: prefer worthless over unreachable.** Wherever there is a
  choice between making a compromise unlikely and making what is compromised
  worthless, take worthless. A safeguard that lowers the probability of theft
  is the weaker answer; one that lowers the value of the thing stolen is the
  stronger, because it keeps holding after the safeguard has failed and after
  whoever was relying on it has stopped paying attention.

  This is the general form of the two rules below it, which are the same idea
  applied to specific cases: state is encrypted at rest so a leaked state file,
  WAL archive or base backup is ciphertext rather than credentials, and backups
  are encrypted to an age _recipient_ rather than a passphrase so the
  automation can write backups it is unable to read. Neither tries to prevent
  the file being obtained. Both make obtaining it achieve nothing.

  In practice this decides design arguments in a specific direction. Narrow
  what a credential is permitted to do before hardening where it is kept: a
  token whose worst use is noisy, reversible and contained is a better answer
  than a better-guarded token that can do real damage. When two designs are
  being compared, state each one's worst case and its actual likelihood rather
  than asserting that either is secure - a worst case nobody has written down
  is a worst case nobody has weighed.

- **A rendered credential is transient, and that is the safeguard.** The
  kubeconfig is written on demand and removed by Sterilize. Its short life is
  the protection, not an inconvenience around it, so nothing may trade that
  away for convenience: do not keep it between uses, do not reuse an existing
  copy rather than regenerating one, and do not put `KUBECONFIG` in a shell
  profile or anything else that assumes it persists. A command that prefers a
  stale credential on disk over minting a fresh one has swapped the safeguard
  for two saved seconds.

  The supported form is `contractor kubeconfig -site <site> -- <command>`,
  which renders into a mode-0600 file outside the repository, runs the command
  with `KUBECONFIG` pointing at it, and removes it on every exit path including
  a signal. `contractor kubeconfig` with no command still writes into the
  workspace for the cases that need a file, and says so.

- **Deletion is not the security property; being worthless is.** Sterilizing
  the workspace assumes the delete happened and that nothing copied the file
  first. So the Backup phase never writes plaintext state to disk at all - it
  pipes `tofu state pull` straight into `age` and wipes the buffer - and state
  is encrypted at rest with OpenTofu's own state encryption, keyed from
  1Password. The whole `encryption` block is carried in `TF_ENCRYPTION`, set by
  break-ground before the first phase, so nothing in git reveals the scheme or the
  key and a bare `tofu` run cannot read state at all. That is the lock, not a
  side effect - and it matters more than protecting the local file, because the
  state _is_ the Postgres database, which CloudNativePG streams to object
  storage continuously under nothing but gzip. Encrypting the state makes the
  WAL archive and the base backups ciphertext too. See
  [`docs/state-and-secret-rotation.md`](docs/state-and-secret-rotation.md) for
  what a leaked state file exposes, the manual cutover for an estate that
  already has unencrypted state, and the rotation runbooks for everything
  encryption cannot cover.
- **State migrates local -> Postgres, and is backed up twice.** The first
  apply runs on local state; once the cluster hosts Postgres, state moves
  there and the local copy is destroyed. CloudNativePG streams WAL and base
  backups to object storage for point-in-time recovery, and the Backup phase
  writes a standalone age-encrypted state dump alongside them. Both are
  needed: the database backups restore into a running cluster, but rebuilding
  that cluster needs the state held in the database. The standalone dump is
  what breaks that circle after a total loss.
- **A run proves the cluster converged before it trusts it.** The Health
  phase sits between Cluster and Migrate and refuses to continue unless every
  node is Ready and counted, every Flux Kustomization and HelmRelease has
  reconciled, and the state database has all the instances it asked for. It is
  there because the first successful ignition was not one: it reported success
  over a database running two of its three instances, because every phase had
  done its own job and nothing was asking the question the phases together
  were supposed to answer. A port that answers is the weakest evidence
  available - it is true from the moment one instance is up. Migrate is the
  point of no return, so the gate goes before it, not at the end.

- **The workspace is sterilized on every exit.** Success or failure, no
  secrets and no state are left on the workstation. On failure the run
  destroys infrastructure _before_ wiping state, so nothing is orphaned.
- **Pin everything.** Actions to commit SHAs, providers to `~>` ranges, Talos
  to an exact version plus Factory schematic ID.

## The button

One entrypoint, a Go program, run from the Linux workstation:

```sh
./scripts/install-dependencies.sh   # once
task start SITE=site0               # builds break-ground and prints the command to run it
./toolshed/contractor break-ground -site site0 # the actual run - always run this directly, never through task
```

**`task start` deliberately does not run break-ground itself.** `task` intercepts
Ctrl-C for its own purposes but does not proxy the signal to the process it's
supervising - a confirmed, currently-open upstream limitation
(`go-task/task#1408`). Ignite's own destroy-then-sterilize cleanup on
interrupt only runs if something actually delivers it the signal, so the
real ignition run has to be invoked directly. Every other `task`-wrapped
break-ground phase (`render-secrets`, `verify`, `configure-hypervisor`,
`backup-state`, `kubeconfig`, `clean-secrets`) stays safe to wrap regardless, because none
of them can reach the Compute phase - an interrupted one leaves stale
secrets at worst, recoverable with `task clean-secrets`, never an orphaned
VM.

**Changing a running estate is `contractor converge`** (`task converge`). It
renders, attaches to the state already in the cluster, and applies - the same
phases as ignition minus `hypervisor` and `migrate`, plus `attach` in front.

Attach is where the safety lives. It refuses if local state exists, because
that means an ignition stopped before Migrate and this workspace already holds
the authoritative copy. It uses `init -reconfigure`, never `-migrate-state`,
because migration is the verb that copies one state over another. And it
refuses if the backend it attached to is **empty**: an init against an empty
backend succeeds exactly as loudly as one against a populated backend, and
applying afterwards would build a second estate beside the first, with the same
names, VM ids and addresses. Nothing can tell those two situations apart from
inside, so it stops.

A converge also never destroys on failure. Ignition's failure path tears down
what it built because a half-finished ignition leaves VMs nobody tracks; a
converge starts from an estate that was already running, so answering a
transient failure by destroying production would be exactly wrong.

**Seeing what a change would do is `contractor plan`** (`task plan`). It renders,
attaches to the estate's state, plans, and prints what would change - then
sterilizes. It is the half of the review a pull request could not give:
approving a diff of HCL used to mean finding out what it meant afterwards.

**A plan shows a change to the control plane's size, and it did not always.**
`data.talos_cluster_health` is scoped to the nodes the config _asks for_, which
is fully known at plan time, so raising `control_plane_count` once made it wait
for machines that did not exist yet and time out producing nothing. The gate now
depends on the VMs themselves, and OpenTofu defers a data source read to apply
time when something it depends on has pending changes - so the read moves to
where the machines exist and the plan completes.

The stopgap was a check in `contractor plan` that compared the config's count
against the state's and refused up front, on the stated ground that this was the
one question a plan could not answer. That was never true of the tool, only of
one line of scoping - and the claim outlived the defect. The refusal stayed after
the fix landed, so the fix was never exercised and the estate's own acceptance
test could not get a plan. Describe a limitation as current and name what would
change it; `docs/epochs/01-ignition.md` records both halves.

**It reports structure and never a value.** A plan holds every attribute of
every resource it touches; this repository keeps hostnames, addresses and
credentials out of git on purpose, and the repository is public, which makes an
Actions job summary and a pull request comment world-readable by anyone. So the
output is addresses and verbs and nothing else - the same line `check-inventory`
draws between "the reference resolves" and "here is what it resolved to", for
the same reason: the output is most useful exactly when somebody wants to paste
it somewhere. The saved plan file is removed by the phase that made it, and
listed in Sterilize for the run that dies first.

**Tearing it down is `contractor demolish`**, run directly for the same
signal-handling reason `start` is. It renders the config first, which is the
credential check rather than a formality - no 1Password session means no
Proxmox token and no hypervisor endpoint, so the command is inert in the
hands of anyone without vault access. `-confirm` must name the site a second
time, which is the guard against a typo by somebody who does hold it.
Interrupting a destroy deliberately does _not_ sterilize: wiping state
part-way through a teardown is how VMs get orphaned, so it waits for tofu to
release its lock and leaves everything in place to be re-run.

**Getting state back is `contractor restore`.** It fetches the break-glass
identity, decrypts the latest age backup from object storage, checks that what
came back is state describing something, and pushes it through the encrypted
backend. It refuses if local state already exists. It is the only place in the
program that reads the private half, which `tests/go/repo` enforces, and it
restores state and stops - what to do with it afterwards is a judgement call,
not a next step.

**Checking the vault is `contractor check-inventory`** (`task check-inventory`). It proves
every `op://` reference in `config/management.tpl.json` resolves and is
non-empty, and reports each one as `ok` / `empty` / `missing` — structure only,
never a value, so the output is safe to paste into an issue or a pull request.

It is `AssertRenderedConfigComplete` shifted as far left as it goes. That check
compares the template against an already-rendered config, so it can only speak
after Render has pulled every secret in the estate onto disk — correct, and the
most expensive possible moment to learn a field was misspelled. This asks the
same question having written nothing. It earned its place: the template carried
a reference to `op://homelab/source-control/token`, an item that did not exist,
which would have failed every run at Render.

`empty` is the quieter half and the reason the check is not just an existence
test. `op inject` treats a blank field as success and writes an empty string,
so an empty field does not fail Render at all — it surfaces much later inside a
provider as something like "credentials are empty", naming no field.

**Agent commits are published with `task push`** (`scripts/signedpush`), which
is beside break-ground and never inside it — different job, different blast radius,
and a bug in a push tool has no business living in the binary that can destroy
the estate. Both are zero-dependency Go for the same reason: this one reads
the GitHub App private key, so a supply chain that reaches it can mint tokens
for this repository.

A GitHub App cannot hold a signing key — SSH and GPG signing keys are
user-account resources, and the agent deliberately has no user account. What
an App can do is have GitHub sign for it: a commit created through the Git
Data API with an installation token comes back signed with GitHub's own key.
A plain `git push` is refused by the `pre-push` hook (`scripts/gatehouse`),
because it produces unsigned commits attributed to whatever local git config
says. That used to be documented and left to discipline, and discipline was not
enough — a session pushed twice with plain git before anyone noticed, and
`non_fast_forward` meant the commits could not be repaired by anybody
afterwards. The guard is structural rather than a flag: signedpush pushes to
`refs/signing/<tmp>`, a plain push updates `refs/heads/<branch>`, and only the
latter is refused.

The naive form uploads a blob per changed file. This does not: `git push`
moves the objects as one packfile to a ref outside `refs/heads/`, and since
git and GitHub compute identical SHAs the tree is then already addressable, so
the API work is a constant three calls at any diff size.

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
IDs - so `site10-cp-100` lives at `10.10.10.100` as VM `10100`. Octets are
asserted unique and
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
`hypervisor`, `overlay_network`, `object_storage` (each with its own asserted
`provider`) and `database`. Anything describing the whole fleet stays at the
top: `organization` and `source_control`, because one repository drives every
cluster through Flux, and `state_backup`, because one break-glass key covers
the estate. Each site runs its own cluster with its own database, so sharing
that password across sites would mean compromising one reaches all - the
backup recipient is the exception precisely because it is a public key, and
sharing it costs nothing.

Hostnames, credentials and even the site's human name are `op://` references,
so the file shows the shape of the estate without revealing what or where
anything is. Every one of them must be wrapped in `{{ }}` - `op inject`
substitutes nothing else, and a bare `op://` string is valid JSON that reaches
the reader verbatim. `tests/go/repo` asserts it.

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

- **Open every pull request as a draft, and let the operator elevate it.**
  Draft is the working state. Checks run on drafts exactly as they do on ready
  ones, so all the iterating happens there: a lane fails, it is fixed, it fails
  again. Marking it ready is a deliberate act by the person who decides, and it
  is what asks the clerk to read the change.

  The reason it is not the agent's call is worth stating plainly, because the
  agent will not notice it about itself. **It is a poor judge of when it has
  finished, and it always believes it has.** Every pull request in this
  repository has been opened in the belief that it was complete, and a fair
  number have then been pushed to three or four more times. A signal that means
  "done fussing" cannot come from the party that is doing the fussing.

  So the sequence is: open a draft, iterate until every check but the human is
  green, the operator elevates, the clerk reads it once and posts what it
  found, the operator approves, the operator merges. The clerk sits between the
  last push and the approval because that is the only point at which its
  findings are still actionable.

  Asking again is a comment, in the Dependabot shape:

  | Command           | What it does                                       |
  | ----------------- | -------------------------------------------------- |
  | `@clerk snag`     | walk the work again                                |
  | `@clerk handover` | read it again as a stranger who has just cloned it |

  It reacts with eyes when it starts and a thumbs up when it is done, and with
  a confused face if the command is not one it knows. **This works on a pull
  request that has already merged**, because `refs/pull/N/head` outlives the
  branch - so the clerk can be pointed at work that landed before it existed.

  Re-requesting a review deliberately does **not** trigger it. That was tried
  and removed: `review_requested` fires on any reviewer request, `CODEOWNERS`
  requests one automatically the moment a pull request is elevated, and a single
  elevation therefore ran the clerk twice.

  And **the clerk can stop nothing**: its findings are code scanning alerts at
  note level, and that check stays out of `main`'s required checks deliberately.
  A criticism that blocks a merge is an outside party acquiring a power this
  design withholds.

  The inspector's `tally` runs on every push regardless, because it costs
  nothing and its value is being current. It says nothing unless the change
  took something away.

- **Branch per epoch, and work merges into the epoch rather than into `main`.**
  `epoch/<nn>-<slug>` targets `main` and stays open for the life of the epoch.
  Individual pieces open against **that branch as their base**, not against
  `main`. Set the base deliberately; defaulting to `main` breaks the model.

  The point is to keep the number of pull requests landing on `main` small
  while still reviewing each piece on its own. A reviewer sees one change at a
  time, and `main` sees one coherent epoch.

  Two consequences worth stating, because both have bitten:

  - **Bugs become issues, and fixes batch.** Open an issue for a defect rather
    than only describing it in a pull request body. Fix issues on their own
    branches, then merge several together into one bug-fix pull request that
    closes them all — individually tracked, few landings. Reference the issue
    number so the link survives the branch.
  - **Merging needs the branch up to date**, so GitHub's "Update branch" lands
    a merge commit on the epoch branch. That is normal and expected. It also
    means an epoch branch **must not** require linear history, which would make
    that button unusable.

- Close an epoch by filling in its record in `docs/epochs/` **before** merging.
- **No `Co-Authored-By: Claude` trailer.** Claude commits here under its own
  git identity, so that trailer credits the same party twice and GitHub shows
  an author avatar beside a redundant co-author badge. It exists to credit a
  contributor who is not the author. This contradicts the Claude Code
  harness's default instruction, which assumes a human is committing the
  model's work — the reverse of the arrangement here — so `commitlint.config.js`
  enforces it rather than leaving it to be remembered. A trailer naming a
  human co-author is still correct and still passes.
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
- **A test states every input it depends on.** Anything read from the machine
  rather than set by the test — git config, environment variables, the working
  directory, the clock, the network — is a bug waiting for a different machine.
  This is not theoretical: two tests in `scripts/gatehouse` were written this
  way in consecutive changes. One built a deliberately _unsigned_ commit and
  inherited `commit.gpgsign` from the developer's global config, so on any
  machine following this repository's own setup the fixture was signed and the
  test failed while the code was correct. The other relied on the pre-push
  environment variables being _absent_, so it passed in a shell and failed
  inside the hook it exists to protect — on a workstation, mid-push.

  Both reported the machine rather than the code. Where a test shells out to a
  tool that reads user configuration, neutralise that configuration rather than
  hoping it is absent; `GIT_CONFIG_GLOBAL` and `GIT_CONFIG_SYSTEM` pointed at
  `/dev/null` do it for git, and `tests/go/repo` asserts any test package
  invoking git does so.

- **Prove a test fails for the reason it claims.** Breaking the code and
  watching something go red is not enough — check that _this_ assertion is what
  caught it. The second failure above passed for years' worth of runs through an
  unrelated error path, so deleting the behaviour it named would not have
  failed it. A test that passes by accident is worse than none: it reports
  coverage that does not exist, and it hides the moment the rule underneath it
  changed.

- **Tests are written before the code they test, in every language this
  project uses one for.** The same shift-left reasoning as formatting, one
  step further left: a defect caught while writing the test is cheaper than
  one caught by `task validate`, which is cheaper than one caught by CI,
  which is far cheaper than one only caught by running real infrastructure -
  the actual, repeated failure mode in this project's own history (the
  self-healing-import fix, the storage-sizing bug, the first full ignition run).
  Go logic in `scripts/contractor` is tested with the standard library's own
  `testing` package - no third-party assertion library, because that module
  has no third-party anything, and keeping it that way is what stops a test
  dependency ever reaching the program that can destroy infrastructure. The
  tiers that need a real estate live in their own module and do use
  Terratest; see `tests/README.md` for why that separation matters. OpenTofu
  logic (`locals`, `precondition`/`postcondition` blocks) is tested with
  OpenTofu's own native `tofu test` and `.tftest.hcl` files - not a custom
  harness, the same vendor-provided tool this project already runs. Both run
  in `task test`, so a broken test is caught in the same
  seconds-not-minutes place formatting already is.
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
