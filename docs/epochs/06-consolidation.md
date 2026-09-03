# Epoch 06 — Consolidation

- **Tier / path:** repository-wide
- **Branch:** `epoch/06-consolidation`
- **PR:** #
- **Status:** Not started
- **Closed:** <date>

## Goal

Go over the whole repository with a fine-tooth comb and remove what has
accumulated. Every other epoch adds; this one is the only one whose success is
measured in things that are gone, and it exists because a repository that only
ever grows becomes one nobody can hold in their head.

At the end, every mechanism here should be one somebody could justify out loud,
and there should be one way to do each thing rather than two that drifted apart.

## The four questions

Every file, workflow, script and check gets asked the same four things. They are
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

## Decisions

_To be filled in as the epoch runs._

## Outcome

_To be filled in at close._
