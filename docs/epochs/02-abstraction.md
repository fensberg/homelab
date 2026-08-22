# Epoch 02 — Abstraction

- **Tier / path:** `modules/`
- **Branch:** `epoch/02-abstraction`
- **PR:** —
- **Status:** Not started

## Goal

Write the reusable pieces once. Turn the one-off resources proven in epoch 01
into parameterized modules that staging and production can both consume
without copy-paste.

## Scope

In scope (per `README.md`):

- `modules/infrastructure/` — OpenTofu modules with `main.tf` / `variables.tf`.
- `modules/applications/` — Kubernetes bases for Flux/Kustomize overlays.

Explicitly out of scope:

- Per-environment values and overlays — epoch 03. A module that knows it is
  "staging" is not a module.
- Changes to `management/` — epoch 01.

## Known driver: multi-site deployment

The management root is currently hardcoded to one site. `base_cidr`,
`node_count` and the derived node addresses are locals in `variables.tf`, and
`organization.name` becomes the Talos cluster name. Deploying a second site
from the same code would advertise a colliding subnet onto the tailnet and
name its cluster identically.

Turning those into module inputs is the concrete reason this epoch exists.
The likely shape is one config file per site rather than one shared template,
so the subnet, cluster name and overlay-network credentials all vary together.

## Open questions to settle first

- Which epoch-01 resources genuinely want to be modules, versus staying
  single-use in `management/cluster`?
- Module versioning: relative path in-repo, or tagged and pinned?
- Does the self-hosted runner have what these modules need at apply time?
  `deploy-infrastructure.yml` path-filters on `modules/infrastructure/**`, so
  a change here triggers a real apply.

## Decisions

_Record as made._

## Outcome

## Deferred

## Gotchas
