# Environments and promotion

What is allowed to change, where, and on what trigger. This is a design note
rather than a plan: nothing here is built yet, and the point is to decide the
shape before the shape decides itself.

## Three different things get promoted, and they are not alike

Most confusion about environments comes from treating these as one problem.

|              | What it is                             | Changes      | Promotion unit               |
| ------------ | -------------------------------------- | ------------ | ---------------------------- |
| **Workload** | The services that do the work          | Often        | A container digest           |
| **Platform** | The cluster, its operators, its config | Rarely       | A site                       |
| **Estate**   | Organization, vault, break-glass key   | Almost never | Nothing - it is the boundary |

The workload model below is well-trodden. The platform model is where most
homelabs and plenty of companies get it wrong, and it is the part worth
thinking hardest about.

## Workloads: staging tracks main, production tracks a pin

This is the standard promotion pipeline, and the intuition behind it is right.

**Staging deploys on merge to `main`.** It exists to break. Its value is
entirely in fidelity: if it does not run the same services, talking to each
other, continuously, then it is not telling you anything about production and
it is a costume rather than an environment. A staging cluster that nobody
exercises is worse than none, because it manufactures confidence.

**Production deploys when a version pin changes**, and the pull request that
changes it contains nothing else. One line, one review, one thing that can
have gone wrong. That constraint is doing real work: it means the diff a human
approves for production is legible in five seconds, and a rollback is the same
diff backwards.

### Promote the artefact, not the source

This is the detail that separates a promotion pipeline from a re-run, and it is
the one outside reviewers look for first.

Production must reference **the same immutable artefact staging tested** - a
digest, not a tag, not a rebuild from a git tag. Rebuilding "the same" source
produces a different image: base layers move, timestamps differ, a transitive
dependency resolves differently. If production rebuilds, then whatever staging
proved was proved about a different artefact, and the pipeline is theatre.

Build once, test that build, promote that build. This estate already does the
digest half of this for the runner image, which is the same instinct.

### The promotion pull request should be generated

If a human types the new version into a file, the pipeline has a typo-shaped
hole in it and the promotion is only as good as the copy-paste. The usual
answer is automation that opens the pull request - Flux's image automation,
Argo CD's image updater, or a small workflow - with the human approving rather
than authoring. The rule "one pull request, one version bump" survives; the
hand-typing does not.

### Where promotion pipelines actually die

Not in the deploy. In the data.

A version pin makes rollback trivial for stateless services and impossible for
anything that has migrated a database, because the old code cannot read the new
schema. The discipline that fixes this is **expand and contract**: a migration
adds without removing, both versions run against the intermediate schema, and
the removal ships in a later release once rollback is no longer wanted. Skipping
it means a pinned production that cannot actually go backwards, which is the
property the pin was bought for.

## The platform: why there is no staging cluster here

The instinct is to mirror the workload model - a staging management plane and a
production one. For a single-site estate that is the wrong shape, and saying why
matters more than the conclusion.

A staging cluster is only useful to the degree it is identical, and a second
management plane on the same hypervisor shares the thing most likely to be the
problem. It doubles the cost of the most expensive tier to run, to test changes
that arrive rarely, and it tends to drift from production precisely because
nothing depends on it. That is how organisations end up with a staging cluster
whose green result nobody believes.

What replaces it, and what enterprise practice actually looks like:

**Disposable rebuilds.** Tearing the estate down and igniting it from nothing is
a higher-fidelity test than a long-lived staging cluster, because it proves the
path that has never otherwise been exercised - the one from zero. A staging
cluster that has been up for six months proves nothing about ignition.

**Plan before apply, with the plan reviewed.** For infrastructure, the plan is
the test. It is the artefact that says what will change before anything does.

**Convergence gates.** A converge that cannot prove the cluster is healthy
should not proceed. This estate's Health phase is that gate, and it sits before
the point of no return rather than at the end.

**The second site is the staging environment.** This is the real answer, and it
is why it does not exist yet. Once an estate has more than one site, a platform
change rolls to one site, is verified, and then rolls to the rest. The promotion
unit for a platform is a _site_, not a namespace - which is exactly what
[`02-abstraction.md`](epochs/02-abstraction.md) is building towards. With one
site there is nothing to promote through, and pretending otherwise by adding a
second cluster on the same hardware buys the ceremony without the isolation.

So: **the management plane is a single production system**, changed rarely,
always through a reviewed plan, and verified by a gate rather than by a mirror.
That is a defensible position rather than a compromise, and it becomes a
progressive rollout the day a second site exists.

## When each thing is allowed to change

| Trigger                                   | Changes              | Gate                                    |
| ----------------------------------------- | -------------------- | --------------------------------------- |
| Merge to `main` touching `environments/`  | Staging workloads    | Pull request review                     |
| A pull request bumping the production pin | Production workloads | Review, and staging having been healthy |
| Merge to `main` touching `management/`    | The platform         | Reviewed plan, then the Health gate     |
| Tag `v*`                                  | Nothing by itself    | It marks; it does not deploy            |

The fourth row is worth stating because it is easy to get backwards. A tag is a
label on a commit, and a commit is not an artefact. Deploying "the tag" invites
the rebuild problem above. The tag records which digest was promoted; the digest
is what runs.

## What an outside reviewer would criticise

Written down because it is cheaper to hear now.

- **A staging environment that is not exercised.** The single most common
  finding. If nothing calls the services, staging is a screenshot.
- **Promotion by rebuild rather than by digest.** Discussed above.
- **No rollback that has ever been performed.** A rollback path nobody has run
  is a hypothesis. It belongs in the same category as an untested backup.
- **Migrations that make the pin a lie.** Expand and contract, or admit
  production cannot roll back.
- **A "staging" that shares the failure domain of production.** Same host, same
  network, same storage. It will be green when production is about to fall over.
- **Environments distinguished by branch rather than by configuration.** Long
  lived branches per environment drift, and the diff between environments stops
  being reviewable. Configuration in one branch, differing by overlay, is the
  practice that held up.

The thing that would _not_ draw criticism is the shape of the workload pipeline
described here. It is ordinary, and ordinary is the goal.
