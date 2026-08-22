# Epoch 03 — Workload

- **Tier / path:** `environments/`
- **Branch:** `epoch/03-workload`
- **PR:** —
- **Status:** Not started

## Goal

Stand up staging and production as thin pointer configs over the epoch-02
modules, and make the promotion path real: merge to `main` deploys staging, a
`v*` tag deploys production.

## Scope

In scope (per `README.md`):

- `environments/staging/{infrastructure,applications}/`
- `environments/production/{infrastructure,applications}/`
- Flux/Kustomize overlays per environment.

Explicitly out of scope:

- New module functionality — belongs in epoch 02.

## Open questions to settle first

- `deploy-infrastructure.yml` already encodes the promotion model: `main` ->
  `staging`, `v*` tags -> `production`. Confirm GitHub Environments of those
  exact names exist with the right protection rules before the first apply.
- Do staging and production share one cluster with separate namespaces, or
  separate clusters? This decides whether the Flux target paths diverge from
  epoch 01's `clusters/management`.
- Where does per-environment secret material come from? The `op inject`
  pattern from epoch 01 assumes a human at a terminal and does not transfer
  to Flux reconciliation. External Secrets or SOPS is the likely answer.

## Decisions

_Record as made._

## Outcome

## Deferred

## Gotchas
