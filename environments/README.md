# Environments

What runs **on** the platform, as opposed to the platform itself. The tiers
below this one build a cluster; this one is the reason there is a cluster.

## The promotion path is the directory you are in

| Environment  | Reconciled from | Deployed by           |
| ------------ | --------------- | --------------------- |
| `staging`    | `main`          | merging               |
| `production` | a `v*` tag      | tagging, deliberately |

Two Flux sources, not one: `flux-system` is pinned to a branch, and
`flux-production` tracks tags by semver. Pointing production at a different
directory of the same branch would deploy to production every time anything
merged, which is a promotion path in name only.

**Production is not ready until the first tag exists, and that is correct.** A
semver ref with no matching tag reports its source as failed. That reads as a
red light and is an accurate one - nothing has been released yet. It is not a
reason to point production at a branch.

## `applications` and `infrastructure`

`applications/` is reconciled by Flux from this repository. `infrastructure/` is
OpenTofu, applied by `deploy-infrastructure.yml`, which is path-filtered to
`environments/**` and `modules/**` so ignition changes never trigger it.

One cluster hosts both environments, separated by namespace rather than by
hardware - see the tier table in `CLAUDE.md`. A directory here is an overlay,
not a machine.

## Secrets

Estate secrets - names, tokens, storage keys - live in 1Password and reach the
cluster through OpenTofu. Workload secrets belong in OpenBao, which does not
exist yet; see #345 for the split and for why a workload in the untrusted zone
can never fetch its own, whichever store they end up in.
