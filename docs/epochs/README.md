# Epoch log

The durable record of this project across sessions. Chat sessions are
ephemeral; this directory is not. Each epoch is a bounded phase with its own
branch, PR, and record of _why_ things are the way they are.

**Read this file first.** Then read the record for the current epoch, and any
earlier epoch whose decisions you are about to touch.

## State of the world

- **Current epoch:** 01 — Ignition
- **Built:** the phased ignition button (`scripts/Start-Homelab.ps1`), an
  idempotent Proxmox playbook, Talos + Flux provisioning, codified Tailscale
  route auto-approval, and an age-encrypted R2 state backup.
- **Open decision:** which Postgres to run on the cluster. Phase 7 (Migrate)
  cannot complete until that exists. See [01-ignition.md](01-ignition.md).
- **Not yet built:** `modules/` and `environments/` — both referenced by
  `README.md` and by the path filters in `deploy-infrastructure.yml`.

## Epochs

| #   | Name        | Tier / path     | Status      | Record                                 |
| --- | ----------- | --------------- | ----------- | -------------------------------------- |
| 01  | Ignition    | `management/`   | In progress | [01-ignition.md](01-ignition.md)       |
| 02  | Abstraction | `modules/`      | Not started | [02-abstraction.md](02-abstraction.md) |
| 03  | Workload    | `environments/` | Not started | [03-workload.md](03-workload.md)       |

## Working an epoch

1. Branch: `epoch/<nn>-<slug>` off `main`.
2. Do the work. Append decisions to the epoch record _as you make them_ —
   reconstructing a rationale three months later is the expensive part.
3. Before merging: fill in Outcome, Deferred, and Gotchas; flip the status.
4. Merge the PR. Tag `v*` when the change should reach production.

Start a fresh session per epoch rather than one long chat. `CLAUDE.md` plus
this log is enough to bring a cold session fully up to speed.

New epochs: copy [`00-template.md`](00-template.md).
