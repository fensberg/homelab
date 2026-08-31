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

## Decisions

_To be filled in as the epoch runs._

## Outcome

_To be filled in at close._
