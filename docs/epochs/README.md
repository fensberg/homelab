# Epoch log

The durable record of this project across sessions. Chat sessions are
ephemeral; this directory is not. Each epoch is a bounded phase with its own
branch, PR, and record of _why_ things are the way they are.

**Read this file first.** Then read the record for the current epoch, and any
earlier epoch whose decisions you are about to touch.

## State of the world

- **Current epoch:** 01 — Ignition
- **Built:** the phased ignition button (`scripts/steward`), an
  idempotent Proxmox playbook, Talos + Flux provisioning, codified overlay-network
  route auto-approval, and a two-layer state backup story.
- **Database:** CloudNativePG, reconciled by Flux, streaming backups to object
  storage. Declared in `clusters/management/`.
- **Not yet built:** `modules/` and `environments/` — both referenced by
  `README.md` and by the path filters in `deploy-infrastructure.yml`.
- **Not measured:** nothing watches the estate between runs. No metrics, no
  alerts, no scaling thresholds — see epoch 04.
- **Not replaceable:** a control-plane node cannot be replaced on its own yet.
  Identity is keyed correctly as of epoch 01, so a single-node change is now
  expressible; nothing drives it in order — see epoch 05.
- **Who can do what:** Claude runs unprivileged, with no vault access and a
  GitHub identity that can push a branch and nothing else — it proposes, a
  human merges. Enforced by the OS, the absent 1Password account and a
  fine-grained token, not by instructions. See
  [the boundary decision](01-ignition.md#the-agents-boundary-is-enforced-not-agreed),
  which carries the commands to re-verify it rather than trust the write-up.

## Epochs

| #   | Name           | Tier / path                           | Status      | Record                                       |
| --- | -------------- | ------------------------------------- | ----------- | -------------------------------------------- |
| 01  | Ignition       | `management/`                         | In progress | [01-ignition.md](01-ignition.md)             |
| 02  | Abstraction    | `modules/`                            | Not started | [02-abstraction.md](02-abstraction.md)       |
| 03  | Workload       | `environments/`                       | Not started | [03-workload.md](03-workload.md)             |
| 04  | Observability  | `clusters/management/infrastructure/` | Not started | [04-observability.md](04-observability.md)   |
| 05  | Node Lifecycle | `management/`, `scripts/steward/`     | Not started | [05-node-lifecycle.md](05-node-lifecycle.md) |

## Working an epoch

1. Branch: `epoch/<nn>-<slug>` off `main`.
2. Do the work. Append decisions to the epoch record _as you make them_ —
   reconstructing a rationale three months later is the expensive part.
3. Before merging: fill in Outcome, Deferred, and Gotchas; flip the status.
4. Merge the PR. Tag `v*` when the change should reach production.

Start a fresh session per epoch rather than one long chat. `CLAUDE.md` plus
this log is enough to bring a cold session fully up to speed.

New epochs: copy [`00-template.md`](00-template.md).
