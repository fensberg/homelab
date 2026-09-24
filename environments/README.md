# Environments

What runs **on** the platform, as opposed to the platform itself. The tiers
below this one build a cluster; this one is the reason there is a cluster.

## The promotion path is the directory you are in

| Environment  | Reconciled from                                         | Deployed by                               |
| ------------ | ------------------------------------------------------- | ----------------------------------------- |
| `staging`    | `main`                                                  | merging                                   |
| `production` | a release pinned in `clusters/management/releases.yaml` | merging a pull request that moves the pin |

Production does not read this directory from `main`. The fabricator builds
each release from it - the workload's production overlay and module, with the
image just built pinned by digest - and publishes it as an OCI artifact named
for the version the workload reported, plus a counter: `1.0.15-1`, then
`1.0.15-2`. Merging here changes what the next release will contain; moving
the pin in `releases.yaml` is what changes production (#510).

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
