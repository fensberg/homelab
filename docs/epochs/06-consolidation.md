# Epoch 06 — Consolidation

- **Tier / path:** repository-wide
- **Branch:** `epoch/06-consolidation`
- **PR:** #
- **Status:** Not started
- **Closed:** `<date>`

## Goal

Go over the whole repository with a fine-tooth comb and remove what has
accumulated. Every other epoch adds; this one is the only one whose success is
measured in things that are gone, and it exists because a repository that only
ever grows becomes one nobody can hold in their head.

At the end, every mechanism here should be one somebody could justify out loud,
and there should be one way to do each thing rather than two that drifted apart.

## The five questions

Every file, workflow, script and check gets asked the same five things. They are
deliberately blunt, and "it works" is not an answer to any of them.

1. **Does this help?** Not "is it correct" — does it catch, prevent or enable
   something real? A check that has never fired and could not fire is
   decoration.
2. **Can this be consolidated?** Two things doing nearly the same job is two
   versions to keep in step, and that is how the OpenTofu pin drifted two minors
   between CI and the workstation without anyone noticing.
3. **Is there a flow that already does this?** The cheapest new mechanism is the
   one already running. A second container pipeline, a second version file, a
   second way to publish an image — each is a new thing to learn and to maintain.
4. **How can we reduce this while maintaining function?** The function is not
   negotiable; the amount of machinery delivering it is.
5. **What does this silently replace?** An override is not wrong for being an
   override - `core.hooksPath` buys the only interception point that runs before
   pre-commit installs third-party code, and it earns its place. What is wrong is
   an override that is total and silent, because the displaced thing then stays
   present, correct and inert, and both halves look healthy to anyone inspecting
   either one. That is how a `commit-msg` hook sat in `.git/hooks` enforcing
   nothing while `.pre-commit-config.yaml` truthfully said it was installed.

   The rule: **whatever takes a total override owns everything it displaced, and
   something must enumerate what that was.** Note where the guard belongs - the
   kustomize `images:` transformer is the estate's one well-guarded example, and
   its check does not verify the transformer, it verifies the outcome the
   transformer exists to produce. A tag reaching the cluster unpinned is a red
   build whether the transformer was misspelled, mis-scoped or deleted.

   The estate has four members of this class and guards one. See #258.

## Scope

In scope:

- **Retire "ignition" and finish the construction naming.** The verbs were
  renamed - `break-ground` replaced `ignite`, `demolish` replaced `destroy` -
  and the noun was left behind. "Ignition" still names the tier in `CLAUDE.md`,
  the title of epoch 01, and roughly 200 occurrences across 40 files including
  phase comments, workflow names and test names. One concept currently has two
  names, which is exactly what this epoch exists to remove.

  **Proposed replacement: `groundwork`.** It is a real construction term for
  site preparation and foundations, it pairs with the verb that already exists
  (`break-ground` does the groundwork), and it obscures no term of art -
  unlike `converge` and `kubeconfig`, which were deliberately kept because
  practitioners already know them.

  Deliberately not folded into other work. A rename touching forty files makes
  every unrelated diff unreviewable, and this repository's own rule is that
  renaming an established component is its own piece of work. It also has to
  wait for epoch 01 to close, because renaming an epoch while it is being
  signed off is churn at the worst possible moment.

  One instance was **not** deferred, because it was a defect rather than a
  name: `contractor -h` listed `ignite` and `destroy`, verbs the program
  rejects. Following the program's own help produced an error from the program
  itself. Fixed, with a test comparing the help against the verb list, so the
  two cannot drift again - that check is the cheap half of a rename, and it is
  worth having before the expensive half rather than after.

- **Migrate the Semgrep lane's container onto the runner-image flow.** Today
  there are two ways a container enters this repository: `.github/runner-image/`
  builds and publishes one to ghcr, pinned by digest and tracked by Renovate;
  and `pr-validation.yml`'s Semgrep lane pulls one straight from Docker Hub.
  That is question 3 answered badly. The shape is to generalise
  `.github/runner-image/` into a matrix over `.github/images/{runner,semgrep}/`
  and point the lane at `ghcr.io/<org>/semgrep@sha256:...`.

  Be honest about what this buys. It does not remove Docker Hub — the mirror
  job still pulls from it to republish. What it changes is _when_ a Docker Hub
  outage hurts: an image rebuild rather than a required check on every pull
  request. Docker Hub timed out three times in forty seconds on one run and
  failed a required check that had nothing to do with the change under review.

  One constraint that does not bend: the Semgrep lane stays on GitHub-hosted
  runners. It runs on pull requests from forks, and moving it to the
  self-hosted runner would put fork-controlled code inside the estate.

- **Retire Dependabot for self-hosted Renovate.** Dependabot is a stopgap,
  not a choice: it was what the estate could use before it had a cluster to
  run anything on. That trigger has fired, and the deferral in
  [`01-ignition.md`](01-ignition.md) records it.

  This is question 2 and question 3 in the same entry. Two dependency-update
  mechanisms is two to keep in step, and the second one already exists - the
  cluster, running Flux - so the cheapest new mechanism is the one already
  running.

  What Dependabot cannot do at all: a Flux `HelmRelease`'s chart version, and
  a digest-pinned image inside a workflow's `container:` block. Both are
  manual bumps today. What it does badly, which matters more here because it
  accumulates: it commits through GitHub's API and so never runs this
  repository's hooks, so every check a hook normalises becomes an exception
  written to accommodate a bot. `.prettierignore` exists because of exactly
  that. Renovate self-hosted runs somewhere we control, which means it can
  run the same hooks a human does, which means that class of exception stops
  being needed.

- **One set of pull-request pools for the estate, not one per site.** The
  pools (`clusters/core/infrastructure/configs/pull-requests-*.yaml`) are in
  the core every site applies, so each site would run and register three
  pools of its own, named `pr-<pool>-<site>`, while a pull request's jobs
  already ask for one site's by a single estate variable
  (`pull_request_runners_site`). With one site that costs nothing; with a
  second, it is a second set of pools nobody sends work to. Sites share what
  they can: the pools, and what they need around them - the sweeper, its
  namespace and its alerts - live where that variable says and nowhere else.
  The operator's ruling, 2026-10-15: this is consolidation, not the second
  site's work.

- **A register of every total override, against question 5.** The audit's own
  output rather than a check: each override named, with what it displaces and
  what asserts the outcome - or a declared reason it is unguarded. That is the
  shape `tests/mutations.yml` and `scripts/approved-suppliers.yml` already use,
  and it is what makes silence refusable. #258 has the four found so far.
- The workflow lanes, against question 1. Several exist because a tool was
  available rather than because a defect was found.
- The `docs/` tree, which has grown a document per incident.
- The four `task` verbs and what each actually owns.
- Duplicated constants that contract tests currently hold together. A contract
  test is the right answer when a value genuinely must exist twice; it is the
  wrong answer when the second copy could simply be deleted.

Explicitly out of scope (and which epoch owns it instead):

- Anything that removes a safety property. Reducing machinery is the goal;
  reducing what the machinery guarantees is not, and the two are easy to
  confuse when the machinery is annoying.
- Node lifecycle mechanisms — epoch 05.
- Observability — epoch 04.

## Acceptance tests

1. **Dependabot is gone**, not merely joined. `.github/dependabot.yml` is
   deleted, Renovate runs in-cluster, and it has opened and landed at least
   one update in an ecosystem Dependabot could not reach at all - a
   `HelmRelease` chart version or a workflow `container:` digest. Both
   running at once is the state this epoch exists to end, so "Renovate is
   configured" is not the test; "Dependabot is deleted" is.
2. **A dependency bump raised by Renovate passes the Format lane without a
   human running `task fix`.** This is the accumulation half, and it is what
   distinguishes replacing the tool from re-hosting it: an update that
   arrives having run the same hooks a human's commit runs needs no
   exception written to accommodate it.
3. **Every remaining ignore entry names the condition that removes it** -
   `.gitignore`, `.prettierignore`, `.github/super-linter.vars`, and
   `approved-suppliers.yml`'s exemptions. "Never, because a lockfile's format
   belongs to its package manager" is a passing answer. Silence is not. The
   audit driving this is in [`02-abstraction.md`](02-abstraction.md); this
   epoch is where the answer has to exist for all of them.
4. **Every test double exists once.** One fake of each outside thing, and one
   builder per shape of input, shared by every test that needs it: a test says
   only what makes it different. Measured by #575, which added two config
   fields and a routes file and touched the same stand-ins again each time:

   | Double                                       | Copies when #575 merged                                              |
   | -------------------------------------------- | -------------------------------------------------------------------- |
   | A whole config, as a fixture file            | 21 (17 cluster, 4 estate), each restating every field to vary one    |
   | A site built by hand in Go                   | 30 literals across the config tests, the repo guards and the harness |
   | A binary faked on `PATH` (`tofu` and others) | 8 test files, each writing its own shell stub                        |
   | The GitHub API for a `github-script`         | 2 test files, each with its own object                               |
   | A sites map for the address plan             | the module's tests, the estate fixtures, the cluster fixtures        |

   Measured again by #643, which added one block under a site's hypervisor
   (the storage driver's token) and wrote the same five lines fourteen
   times across 13 fixture files, one of which holds two sites. Nothing
   checked that the fourteen agreed with each other, only that each still
   parsed.

   The shapes that pass: a base config plus a per-case patch, merged at test
   time - the pattern [`02-abstraction.md`](02-abstraction.md) records under
   "the config fixture corpus does not scale", now owned here - read by the
   HCL and Go halves alike; one Go builder of a valid site that every Go tier
   imports; one helper that puts a fake binary on `PATH`; one fake GitHub for
   the scripts. **The test is a change, not a count:** adding a config field
   touches the base and nothing else, and a new case is the lines that differ.

   Why it belongs here rather than in each epoch that adds tests: every copy
   was written correctly by the change that needed it, which is exactly how a
   repository accumulates them. And a stand-in drifts the way production code
   does, but silently - a fixture nobody updated keeps passing while asserting
   the wrong shape. The guard that keeps it true is the one
   [`02-abstraction.md`](02-abstraction.md) already asks for, pointed at
   tests: a new fake or fixture builder outside the shared ones is refused
   unless declared, with a reason.

   Measured a third time on 2026-10-08, by what a real plan is handed and
   not by a stand-in. The contractor gives a plan of a root three things the
   committed files do not hold: a token to fetch the site's release with,
   the hypervisors' facts, and the config template with the site's
   applications added. The integration tier plans for itself, on purpose - a
   second plan that agrees is what makes a failure believable - and rebuilt
   each of the three by hand. Each fell behind when the contractor changed
   (#663, #670, #671), and the nightly reported an estate that had changed
   when it had not. The operator: "Of course we want shared inputs... Shared
   pieces are built once - not 15 times." **The shape that passes:** what a
   plan of each root must be handed is declared once and read by the
   contractor and the test harness alike, and the plans stay separate - each
   still runs OpenTofu itself and reads the result itself. That shared piece
   is where the proof belongs that a test given a changed input reports a
   change: built once with it, not as another thing that runs every night.

5. **Nothing unused is kept, and something says so.** Only `go vet` reads
   the Go here, and it does not report a function nothing calls; a fuller
   linter is named in two comments and run by nothing. The guards that
   exist each catch one kind of leftover - an exemption no longer needed,
   a declaration naming something gone, an image nothing runs, a set of
   runners no job asks for (#661, where the operator asked for this: "Shouldn't
   there be a guard against unused, dead code? It's anti-bloat") - and none
   catches plain unused code. The tools for it are built: the Go team's
   `deadcode`, which reports what no program's entry point reaches, and
   `staticcheck`, which reports what is unused inside a package. One of
   them, pinned, as a lane of its own, and what its first run finds taken
   out. **The test is a change:** a function added and never called fails
   a pull request.

### The contractor gives way to standard tools

Agreed 2026-10-15. Reading the contractor against what Epoch 05's roll
needed showed much of it doing, in Go, jobs standard tools already do: it
had become an orchestrator and a secrets renderer and a health checker of
its own. The operator: "It feels like we wasted a lot of time re-inventing
the wheel. I don't like that." So each of its pieces is a criterion here,
and the epoch does not close until each is replaced or is shown to have no
standard equivalent.

Each is done the same way: the standard tool does the job, **what the piece
guaranteed is written down first and still holds afterwards** - by a test,
a guard or the tool itself - and the Go is deleted. A piece moved without
its guarantee is not done.

- **6. Secrets are never rendered to disk.** The config's vault values reach
  OpenTofu through its 1Password provider, as ephemeral values that never
  reach state, and reach Ansible through `op run` and its own 1Password
  lookup. _Keeps:_ nothing secret is ever written to a file, and a value
  that was never written needs no wiping; the Sterilize phase shrinks to
  what remains, or goes. _Replaces:_ render, the rendered secrets, most
  of Sterilize.
- **7. The hypervisor is prepared by Ansible, run directly** -
  `op run -- ansible-playbook` - and not through the contractor. _Keeps:_
  the same play, the same refusals inside it. _Replaces:_ the hypervisor
  phase and the inventory it renders.
- **8. The site's roots are ordered by Terragrunt**, split where a provider
  needs what an earlier root created, and nothing is applied with
  `-target` as routine. _Keeps:_ every step applied in the order the
  steps list gives today, and a plan of the converge's own steps.
  _Replaces:_ the steps list, the targeted applies and per-step plans of
  the compute and cluster phases. The steps list is a workaround for
  using `-target` routinely, which OpenTofu's and HashiCorp's own guidance
  says not to do.
- **9. The health gate is the platforms' own checks**: `talosctl health`,
  `kubectl wait` on Flux's layers and on the state database, run as steps.
  _Keeps:_ every question the gate asks today that the stock checks do not
  (every etcd member voting, the database seen at rest twice, a DaemonSet
  short of a machine), as a wait of its own or with an argument that it is
  not needed. _Replaces:_ health, health_etcd, atrest, most of their 1700
  lines.
- **10. The state is backed up by a scheduled job** of three standard tools,
  `tofu state pull | age | rclone`, with no Go. _Keeps:_ the plaintext
  state never touches a disk, the dump goes to the bucket of its own, and
  a backup that is late is said (epoch 04, criterion 12). _Replaces:_ the
  Backup phase.
- **11. Moving the state from a local file to the database is
  `tofu init -migrate-state`**, run once by the ignition. _Keeps:_ the
  refusal to copy an empty backend over a populated one. _Replaces:_ the
  Migrate phase's own copying.
- **12. A credential lives as long as its command, by `op run`.**
  `talosctl kubeconfig` and `talosctl config` under `op run --`, with
  nothing written that outlives the command. _Keeps:_ a credential is
  never kept between uses. _Replaces:_ the kubeconfig and talosconfig
  wrappers.
- **13. The hypervisor's memory budget is an OpenTofu precondition**, read
  from the Proxmox provider's own data about each host, so it is checked
  at plan by the tool that would make the machines. _Keeps:_ a site is
  refused machines its host cannot hold, at plan time, and the plan of a
  pull request says so. _Replaces:_ the budget package and the hardware
  survey, or the part of it the provider's data covers.
- **14. Each rule about the config lives in the config's own checks**:
  OpenTofu `precondition` and `check` blocks, where the build's and the
  converge's preconditions are really rules about what the config says.
  _Keeps:_ every refusal, with a case in the contract corpus. _Replaces:_
  most of the build and converge preconditions.
- **15. What is left of the contractor is named, and each has no standard
  equivalent**, written down beside it: planning a pull request against
  the as-built record with no credential; taking the record and the
  capacity snapshot; restoring from the break-glass key; and refusing to
  attach to an empty state backend, which nothing standard can tell from
  a full one. A piece on that list that turns out to have a standard
  equivalent goes back above.

Epoch 05's roll of machines is the first piece built the new way: an
Ansible play, one machine at a time, and not a phase of the contractor
(the operator, 2026-10-15). #626's Go was closed for it.

## Decisions

### One enumeration primitive, and the walk root was the stale part

Guards discovered their subjects two ways and the two disagreed. Some asked
`git ls-files`; others walked from the repository root with a hand-written
`skipDirs` map. That fork was not a matter of style. The walk existed because
the mutation ledger proves a guard against a _copy_ of the tracked files, that
copy was not a git repository, and `git ls-files` there exited 128 — so any
guard that had to survive the ledger could not use it.

The reason turned out to be removable: the ledger now runs `git init` and
`git add` in the scratch tree, which costs milliseconds. `trackedFiles` in
`tests/go/repo/tracked_test.go` is now the single primitive, and `skipDirs` is
deleted.

**What that changed conceptually.** "Walk the whole repository" was already the
rule here, and several guards satisfied it by walking from a named subtree —
`clusters/`, `scripts/`, `tests/go/repo` — which buys nothing, because moving
the subject silently shrinks what the guard covers while it stays green. The
sharper rule, and the one the primitive enforces by taking no directory
argument at all: **hardcode what a guard asserts about, never where it looks.**
A declaration list naming specific paths is correct; a walk root never is.

It also answers acceptance test 3 for one of the four files. The exclusion
question is now split in two. Generated directories — `node_modules`,
`coverage`, `.terraform` — are untracked, so `.gitignore` answers for them and
it is maintained for its own reasons rather than as a guard's private list.
What remains is `notAuthoredHere`: files this repository _tracks_ but did not
write (Flux's generated install, `go.sum`, `pnpm-lock.yaml`). That is a claim
about specific subjects, so naming paths in it is right, and a guard requires
every entry to still exist.

**A defect the ledger caught during the change.** Entries that PLANT a file
went green against the planted violation, because the plant happened after the
index was written and an untracked file is invisible to `git ls-files`. The
index has to describe the tree at the moment the guard runs, not at the moment
the copy was made. That is the ledger doing exactly its job: the guard would
otherwise have passed while covering less, which is the failure this whole
regime exists to make loud.

### harden-runner and checkout, written once

`step-security/harden-runner` is pinned in 31 jobs and `actions/checkout` in 30.
The first attempt at consolidating them was a guard requiring every copy to
match a declaration file. It was reverted: it enforced agreement between 91
copies while keeping all of them, and the declaration was a 92nd place the
commit was written. It was also chosen for the wrong reason - it avoided a patch
to a protected path - which this estate has already said is not a reason.

**This had been tried before, and the record of why it failed was wrong.**
`.github/actions/secure-checkout` combined both steps on 2026-08-19 and was
deleted the same day with a commit message citing "clarity and
maintainability". The real reason is in that run's annotations: every job
failed with "Can't find 'action.yml' ... Did you forget to run actions/checkout
before running your local action?" It was referenced with `./`, which is read
out of the workspace, and it was the step that fills the workspace.

**`$/` removes that constraint and nothing else.** GitHub's self-repository
reference, generally available since 2026-07-30, resolves an action at the
running commit with no checkout. So one composite action can now run first.

It lives at `.github/workflows/mobilize/action.yml` rather than under
`.github/actions/`, because it decides what every job may reach and whether a
token is left on disk. Beside the workflows it sits behind the `workflows`
permission the agent deliberately lacks, so changing it takes the same human
hand-over as changing a workflow. GitHub loads workflows only from the top of
that directory, so it is never run as one. The cost is that actionlint's hook
reads the whole tree and mistakes action metadata for a workflow; the hook now
reads top-level files only. zizmor audits it correctly as a composite action.

**The blocker #387 recorded was narrower than stated.** It said the only way to
satisfy both zizmor and actionlint about `$/` was an actionlint ignore covering
the format check for every `uses:`. actionlint's ignore matches the message,
and the message names the reference, so it can be scoped to references into
`.github/workflows`. Tested: with the exemption in place, `uses: someone/else@`
is still reported.

**One thing no document settles, so a lane settles it.** harden-runner installs
its policy in a pre-step (`src/setup.ts`; the main step never reads
`egress-policy`), and GitHub documents `runs.pre` as unsupported for local
actions. If the pre-step does not run when harden-runner is nested, every job
moved onto the action is unwatched and green. harden-runner's own report is not
evidence either: it falls back to audit on several errors and carries on.

So nothing is migrated until `.github/workflows/egress-proof.yml` answers it.
Three jobs, because one proves nothing on its own: the probe host must answer
under audit (so a refusal is the policy, not an outage), must be refused under
block with harden-runner direct (so the method can see a known block), and must
be refused under block with harden-runner inside `mobilize`. Each also requires
an allowed host to answer, so refusing everything cannot pass as blocking
correctly. It stays afterwards as standing proof that a block policy blocks.

A dedicated lane cannot replace per-job hardening, which was considered:
harden-runner watches only the runner it is installed on, and every job gets a
fresh one.

**It passed, and the proof showed more than it asked.** All three jobs were
green, and the nested run lists "Pre Mobilize" and "Post Mobilize" executing
harden-runner's own pre and post steps and checkout's cleanup. So under `$/` a
nested action gets its whole lifecycle - which also settled two wrappers that
depend on a post-step: `create-github-app-token` revokes its token there, and
`setup-go` saves its cache there.

**Then every duplicated action got one home.** Six composites under
`.github/workflows` hold the only literal commit of an action that was written
more than once: `mobilize` (harden-runner, then checkout through the next one),
`checkout`, `setup-go`, `upload-sarif`, `github-script` and `app-token`. Actions
used once stay where they are. `TestEveryActionCommitIsWrittenOnce` refuses a
second `uses:` line for any action path, and two commits across paths of one
repository - codeql-action's `init` and `analyze` are separate paths, and may
not name separate commits.

Three details worth keeping:

- **Checkout is its own action, not only a step of `mobilize`.** The clerk
  resolves which pull request to read before it knows what to fetch, so it
  mobilizes with `checkout: "false"` and checks out later. A second harden-runner
  call would have been wrong, and a checkout written in the clerk would have
  been a second copy.
- **`app-token` refuses a call that names no permission.**
  `create-github-app-token` requests every permission the installation holds
  when given none, so a wrapper whose permission inputs all default to empty
  would silently widen any caller that forgot one.
- **`setup-go` takes no version.** It reads `GO_VERSION` and refuses to run
  without it, so the wrapper cannot become a way to restate the pin.
- **Scripts are files, not inputs.** The first `github-script` wrapper took the
  script as an input and forwarded it as `script: ${{ inputs.script }}`. zizmor
  and Semgrep both reported that line, and they were right about the effect if
  not the line: they treat github-script's `script` as a place code runs and
  check it for template injection, and behind a wrapper they could no longer
  see the callers' code at all. So the plan-comment scripts moved into files
  beside the action, callers name one, and there is no `${{ }}` in any of them
  for injection to hide in.

**Semgrep's `github-actions-mutable-action-tag` rule is excluded.** It exempts
`./` and `docker://` but not `$/`, so it fires on every job. zizmor's
`unpinned-uses` already owns pinning under a blanket hash policy and reads
composite actions as well as workflows - checked with a tag-pinned step in
each - so the rule was a second owner of one check, and the wrong one.

**The direct control job was dropped from the proof.** It existed so a refusal
through `mobilize` could be told apart from a probe that sees nothing. With
that answered, keeping it would be the one place harden-runner is still written
twice. The lane now keeps `reachable` and `nested`, and asserts that a calling
step's env reaches a shared script step - the plan comments read their values
that way and run only on infrastructure changes.

**Scorecard is migrated with publishing still on, knowingly.** OpenSSF's API
rejects results from a job with steps outside its approved action list, and a
`$/` step is not on it. Its documented rules also forbid a workflow-level
`defaults:` block, which `scorecard.yml` has had while runs succeeded, so the
documentation does not predict what is enforced. Scorecard runs only on `main`,
so nothing proves this before merge. If it is rejected, the choice is between
turning publishing off and one declared exception.

### The egress proof is a lane, not a gate, and sudo is the protection that holds

A separate Egress Proof workflow was folded into `pr-validation.yml` as two
lanes. The question it answered - does a nested harden-runner enforce - needed a
real runner, so it could never be a Go test, but it did not need its own file.

Making it a gate every lane waits behind was considered, and so was a
start-of-job check inside `mobilize`, and both were rejected as theater against
the threat that motivates egress control at all:

- **Every job gets its own VM,** so a gate proves nothing about another job, and
  a gate in `pr-validation.yml` cannot reach the workflows holding real
  credentials.
- **GitHub-hosted runners give passwordless sudo,** so a check at the start of a
  job is undone by the compromised step it exists to contain: root can stop
  harden-runner's agent. What makes "enforced at the start" stay true is
  removing sudo, which `mobilize` now requires every job to decide on (#410).
- **harden-runner does nothing on the self-hosted ARC runner.** It writes its
  policy for a StepSecurity cluster agent that is not installed, and a real
  converge's log records no destinations. The jobs holding the vault token were
  labelled `audit` and were neither restricted nor recorded. The proposed fix,
  transcribing their observed destinations and flipping them to `block`, had no
  log to transcribe and would have blocked nothing. That work belongs at the
  cluster's network layer (#411), and a guard now refuses any self-hosted job
  claiming `block` in the meantime.

The lane stays because it is cheap and catches the systemic break: a
harden-runner bump or a change to how GitHub runs nested actions goes red on the
pull request that introduces it. It also checks that sudo really is gone.

### A language nobody declared is a language nothing checks

Every tool in this estate is bound to a file type — shellcheck to `.sh`, tofu
to `.tf`, vitest to `.ts`, hadolint to Dockerfiles. That follows from #365: the
one aggregate that held a default opinion about unfamiliar files was removed
because it hung more often than it caught anything, and each replacement is a
dedicated tool with a dedicated subject.

The cost was never written down. Adding a `.rs` or a `.py` here produced
silence — not vetted, not built, not linted, no coverage floor, no mutation
proof — and nothing reported it, because no check had heard of the extension.
`approved-suppliers.yml` was not the backstop either: nothing reads its
`tools:` section, which its own header admits (#385).

`tests/languages.yml` now declares what reads each kind of file, and
`languages_test.go` enumerates every tracked file and refuses a kind that is
not named. **The bar is deliberately not "a new language must arrive with
tooling"** — that would be worked around. It is that somebody must write down
that it has none, at which point the hole is countable and the ceiling may only
fall.

Its limits are stated in the guard rather than left to be discovered: a
language sharing an extension with a declared one is invisible to it, and so is
a declared kind whose named reader has quietly stopped running. The one
reachable gap — an extensionless script naming an interpreter nothing here can
check — is closed separately by reading shebangs, because `githooks/` is
executable code that runs on every commit and its files have no extension to
classify.

### One version, one lockfile (#416)

A version pin is half a pin. `checkov==3.3.17` fixed checkov and let the
hundred-odd packages it pulls in resolve fresh on every install, within
whatever ranges their maintainers declared; pre-commit's hook repository did
the same inside a virtualenv nobody could see; and the Format lane restated
pre-commit's version in the workflow while `versions.env` held another copy.
Binaries were better only by luck of the hand that wrote each lane: hadolint,
shellcheck and trufflehog had a checksum beside their version, kubeconform had
none, and the OpenTofu installer script was fetched and executed as served.

`scripts/deliveries.lock` is now the one place anything is pinned by hash, for
every tool with no lockfile of its own ecosystem:

- **Versions are decided in `scripts/versions.env` only.** The three
  `*_SHA256` keys left it: a hash is a fact about what a supplier served for a
  version, not a decision, and one written by hand could be anything.
  `.github/actions/versions` now exports only `*_VERSION`, so a checksum key is
  refused by the test that already refuses a key the action would skip.
- **How a tool arrives is one field on its `tools:` entry** -
  `pypi: <package>` or `fetch: <url>` with `{version}` where the publisher puts
  it. trufflehog, kubeconform and OpenTofu gained entries; all three were in
  use and never written down.
- **Ordering writes the lock, security checks it.** `task order-deliveries`
  runs `procurement order` then `security guard-deliveries`. The PyPI tools
  are resolved together, once, for CPython 3.12 (ubuntu-latest) and 3.13
  (Debian 13), and ordering refuses if the two disagree; each section is its
  tool's closure within that one resolution, with the SHA256 of every file
  from PyPI's JSON API. A fetched file is downloaded and hashed by ordering
  itself - one path for every publisher, rather than GitHub's asset digest for
  some and three checksum formats for the rest. The three hashes that already
  existed came out identical, and kubeconform's matched the `CHECKSUMS` file
  its publisher ships.
- **`scripts/take-delivery.sh` is the only installer.** pip with
  `--require-hashes`, or a download refused before anything reads it when its
  hash differs; everything lands in `~/.local` for every caller, so no lane
  needs sudo (#410). `TestNothingInstallsFromPyPIOutsideTakeDelivery` refuses a
  second one.
- **Python tools live in one isolated environment**,
  `~/.local/share/deliveries/pypi`, created without system site packages, with
  each tool's own commands linked into `~/.local/bin` and the environment's
  interpreter there as `delivered-python` (#423). The first version used
  `pip install --user --break-system-packages`, and the first real workstation
  run showed two things wrong with it. pip's `--require-hashes` checks what it
  downloads, not what it finds installed, so Debian's jinja2 satisfied the
  lock with no hash compared. And the user site precedes the OS's own packages
  on `sys.path`, so the locked `packaging` 23.2 replaced Debian's 25.0 for
  every Python program that account ran. `delivered-python` is a two-line
  wrapper rather than a link, because a symlink to a virtual environment's
  interpreter starts the system Python without the environment - checked
  rather than assumed. `install-dependencies.sh` removes what the first
  version left in the user site, and only packages the lock names.
- **pre-commit-hooks became local system hooks** running `delivered-python -m
pre_commit_hooks.<module>` with the upstream 4.6.0 types, stages, args and
  excludes, so its version lives in `versions.env` too. The hookshim guard lets
  such a hook run in the Format lane only when the lane takes delivery of that
  module's package from the lock before running pre-commit.
- **ShellCheck in CI** no longer comes through `ludeeus/action-shellcheck`,
  which downloaded the binary and checked nothing. The lane takes delivery
  and selects files the way the action did, from what git tracks.

Two decisions taken mid-build, recorded on #416. The kind for a fetched file
is `fetch:`, not `release:`, because "release" already names the expedite job
that holds a bypass. And the OpenTofu installer's own hash is locked rather
than the installer being replaced with the release archive: the operator chose
it knowing the cost, which is that the lock goes stale whenever OpenTofu edits
the script, with no version change to explain the refusal. When
take-delivery.sh refuses `opentofu`, that is the cause, and the fix is to order
again.

Out of scope, and why: `go.sum`, `pnpm-lock.yaml` and `.terraform.lock.hcl`
are their ecosystems' own locks; action commits and container digests are
pinned where they are used; and the runner image's downloads (tofu, rclone,
kubectl, talosctl) are built by another pipeline and filed as #421.

### A push costs seconds, and only what cannot be undone runs there

Measured while building #416: committing took about a second, and pushing
took 160. The `pre-push` hook ran `task validate` and the whole of `task
test`, and `tests/go/repo` was 143 seconds of it - 54 in the
comment-stripping re-run and 53 in the mutation ledger, both checks on the
checks, and both run again by the Test lane on the pull request. The push was
paying twice for feedback it would get anyway, while the operator waited.

The operator set the bar: commit and push together under ten seconds, running
only what cannot be undone and what is genuinely fast. What a push must stop is
what no later push repairs - an unsigned commit, a secret, a real name or
address in a public repository. Everything else is a red check fixed by the next
push.

So the hook runs `go test -C tests/go -count=1 -short ./repo`, about six
seconds. A guard that costs whole seconds calls `heavy(t, why)` and skips
under `-short`, which is Go's own mechanism for exactly this rather than a
list somebody keeps.
The skip is honest only while something runs those guards, so
`TestHeavyGuardsRunOnEveryPullRequest` refuses a workflow that passes `-short`,
and the ledger proves it.

**A conflict with main cost a pull request, and no longer does**
(2026-10-11). One line of conflict in the mutation ledger closed #686 and
reopened it as #687, with the operator applying the same workflow patch
twice. The operator: "Our system should work for and protect us, not be one
giant round-a-bout circus that's pedantic." Three causes, none of which
was protecting anything:

- The ledger was one list with a count at its head, so any two changes that
  added a guard conflicted. Each proof is a file of its own under
  `tests/mutations/`, and that directory is a sensitive path: a removed
  proof is a deleted file that opens a review, which is what the count was
  for.
- No branch may be rewritten, and `signedpush` refused every merge, so a
  conflict left a new branch as the only way forward. It refused merges
  because it replayed each commit with one parent, and a merge flattened
  that way republishes what it brought in. It now replays a merge of main
  as a merge, with main's own commit as its second parent, and refuses any
  other. Tried on GitHub before it was relied on: a branch nine workflow
  files behind main was brought up to it, and the merge came back signed
  with both parents. Not tried: a branch that also carries its own applied
  patch while main's workflows have moved.
- The hook's guards answered from Go's kept results, which passed a tree
  on one machine that the same guards refused on another. It runs with
  `-count=1`.

What remains is the patch itself: applying one is five steps for the
operator, and is #688.

### A candidate: one versions file that everything reads, and Renovate driving it

**Not decided.** From [ionfury/homelab](https://github.com/ionfury/homelab),
read at `150097e` on 2026-09-11.

Their `versions.env` is genuinely single: their OpenTofu reads it for Talos,
Kubernetes and the Cilium bootstrap, Flux substitutes it into chart versions,
their upgrade controller reads it, and Renovate updates it through one generic
regex manager over `# renovate:` annotations - the format this estate already
writes, for a Renovate that does not run (#375). Renovate runs self-hosted in
Actions with its own GitHub App, and patch and minor updates automerge only
after a soak (a day for patches, three for minors); majors never do.

**Version holds** are the part worth taking even without the rest. A downgrade
forced by an upstream regression goes into the versions file with an entry in
a holds file naming the constraint, the reason and the upstream issue, and a
weekly job opens an issue here when that upstream issue closes. A hold that
cannot be forgotten is the fail-closed form of a comment saying "pinned because
of a bug".

What would have to differ here:

- **Signed commits.** The "all branches" ruleset requires signatures with no
  bypass, so Renovate needs `platformCommit: "enabled"` to have GitHub sign its
  commits, or every one of its pull requests is refused with no readable
  reason.
- **This estate's own images are not Renovate's to bump.** The fabricator that
  builds a digest records it; asking a bot to rediscover it is circular.
- **Flux cannot read `scripts/versions.env` itself.** It needs a ConfigMap
  generated from it and handed to `postBuild.substituteFrom`, or the file is one
  of two again.
- Renovate is a new supplier, and its workflow arrives as a patch.

It bears on #334 and #339, and on the third-party half of #375.

## Deferred

- **One schema for the config (#567).** The config's shape is implemented
  twice - the HCL's reader and `scripts/contractor/config` - and held together
  by contract tests over a shared fixture corpus. A published JSON Schema that
  the editor, CI and both readers validate against removes the second
  implementation rather than checking it; what a schema cannot say, such as
  unique octets, stays in code as the stated exception. It is the same goal as
  the "two config-contract implementations" entry in `docs/ideas.md`. Found in
  epoch 02's abstraction review, 2026-09-27.

## Outcome

_To be filled in at close._
