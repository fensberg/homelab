# Epoch 14 — Second Site

- **Tier / path:** `config/`, `management/`, `clusters/`
- **Branch:** none; each piece branches from `main`
- **PR:** #
- **Status:** Not started
- **Closed:** `<date>`

## Goal

The estate runs a second site beside site0: its own cluster, its own `/16`,
its own vaults and its own database, added by appending it to `sites[]` and
nothing else. At the end, everything that checks the estate checks every
site it has, not only the first.

Opened on 2026-10-23 to hold what has to be looked at when a second site is
added. The scope is written when the epoch is planned.

## Scope

In scope:

- Every check that reads one site's rendered form judges every site's.

Explicitly out of scope (and which epoch owns it instead):

- A second node in one site: epoch 10, Second Node.

## Decisions

## Outcome

## Deferred

## Gotchas

### The configuration rules over OpenTofu see the sites the template plans

The rules that judge OpenTofu read its plan, made from nothing in the rule
harness with a stand-in for every vault value. The plan is of the sites the
config template declares, which on 2026-10-23 is site0 alone. Agreed then as
enough until a second site exists. When one is added, the harness plans each
site, and a rule that holds for site0 is shown to hold for it too, or the
rules check one site of two and say nothing about the other.
