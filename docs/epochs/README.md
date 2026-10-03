# Epoch log

The durable record of this project across sessions. Chat sessions are
ephemeral; this directory is not. Each epoch is a bounded phase with its own
branch, PR, and record of _why_ things are the way they are.

**Read this file first.** Then read the record for the current epoch, and any
earlier epoch whose decisions you are about to touch.

## State of the world

- **Open epochs:** 03 — Workload, 04 — Observability and 08 — Agent Roles,
  all in progress at once. Epoch 03 was brought forward because the game
  server named as its success criterion needed enforced NetworkPolicy before
  it could run at all; epoch 04 built the monitoring stack (#435). Epoch 01
  signed off 2026-09-03. Epoch 02 closed 2026-10-02: it carried the
  estate/site split, the as-built record and the release a site pins. Epoch
  05 is next: replacing the machines a site has, all of it provable on one
  node. Taking up a second node was cleaved from it on 2026-10-03 into
  epoch 10, which waits on a second node being bought.
- **Built:** the site lifecycle program (`scripts/contractor`) and the
  estate's (`scripts/lawyer`), each holding only its own scope's credentials;
  an idempotent Proxmox playbook, Talos + Flux provisioning, codified
  overlay-network route auto-approval, and a two-layer state backup story.
  Pull requests are planned against the as-built record on GitHub's runner and
  hold no credential, so no environment has a reviewer and the pull request's
  approval is the only gate (#554, #558).
- **Database:** CloudNativePG, reconciled by Flux, streaming backups to object
  storage. Declared in `clusters/management/`.
- **Not yet built:** `modules/infrastructure/` and
  `environments/*/infrastructure/` — both referenced by `README.md` and by the
  path filters in `deploy-infrastructure.yml`, and neither exists on any branch.
  The applications halves of both do exist, built by epoch 03 for the game
  server. The provider split that has to come before the module carving is
  designed but not built — see
  [02-abstraction.md](02-abstraction.md#the-split-is-two-roots-sharing-one-config-and-the-seam-is-two-values).
- **No longer disposable, but recoverable:** the cluster holds a Valheim world
  that players brought with them. It is backed up hourly to the site's
  production bucket and restored before the server starts, which is how it
  came back after the estate/site rebuild of 2026-09-25. A rebuild is still
  not free - it costs up to an hour of the world - so re-cost anything that
  reaches for one.
- **Measured, partly:** kube-prometheus-stack runs in the cluster and routes
  alerts to the estate's alerting webhook (#435). What it answers, and what it
  does not yet, is in [04-observability.md](04-observability.md).
- **Partitioned by affinity, not by taint:** `Taints: <none>` on every node
  still, but CI and all three operators now carry a **required**
  anti-control-plane node affinity and a priority class, so nothing this
  repository deploys lands on the machines holding etcd quorum. That is the part
  that needed no storage answer. The taint itself moved to epoch 03 with the
  state database it is blocked on — see
  [03-workload.md](03-workload.md#storage-which-is-the-quieter-problem). Before
  this, CI ran on the control planes and it cost one killed job (#236). The
  measurements and the model are in
  [02-abstraction.md](02-abstraction.md#known-driver-the-estate-runs-at-a-few-percent-and-the-fuse-is-memory).
- **Not replaceable:** a control-plane node cannot be replaced on its own yet.
  Identity is keyed correctly as of epoch 01, so a single-node change is now
  expressible; nothing drives it in order. Epoch 05 has the converge order
  Talos's own upgrade and graceful reset, one machine at a time; Cluster API
  is deferred.
- **Who can do what:** Claude runs unprivileged, with no vault access, and
  publishes as a GitHub App that can push signed commits and open pull
  requests but cannot approve one or change `.github/workflows/` - it
  proposes, a human approves and merges, and workflow changes arrive as
  patches the human applies. Enforced by the OS, the absent 1Password account
  and the App's permissions, not by instructions. See
  [the boundary decision](01-ignition.md#the-agents-boundary-is-enforced-not-agreed),
  which carries the commands to re-verify it rather than trust the write-up.

## Epochs

| #   | Name           | Tier / path                           | Status      | Record                                       |
| --- | -------------- | ------------------------------------- | ----------- | -------------------------------------------- |
| 01  | Ignition       | `management/`                         | Complete    | [01-ignition.md](01-ignition.md)             |
| 02  | Abstraction    | `modules/`                            | Complete    | [02-abstraction.md](02-abstraction.md)       |
| 03  | Workload       | `environments/`                       | In progress | [03-workload.md](03-workload.md)             |
| 04  | Observability  | `clusters/management/infrastructure/` | In progress | [04-observability.md](04-observability.md)   |
| 05  | Node Lifecycle | `management/`, `scripts/contractor/`  | Not started | [05-node-lifecycle.md](05-node-lifecycle.md) |
| 06  | Consolidation  | repository-wide                       | Not started | [06-consolidation.md](06-consolidation.md)   |
| 07  | Metered Egress | `clusters/management/`, `scripts/`    | Not started | [07-metered-egress.md](07-metered-egress.md) |
| 08  | Agent Roles    | `.github/`, `scripts/`                | In progress | [08-agent-roles.md](08-agent-roles.md)       |
| 09  | Access         | `management/estate/`                  | Not started | [09-access.md](09-access.md)                 |
| 10  | Second Node    | `management/`, `scripts/contractor/`  | Not started | [10-second-node.md](10-second-node.md)       |

## Working an epoch

1. Branch each piece of work off `main`, and open its pull request against
   `main`. Epoch branches are retired; an epoch is now a record and an issue
   label, not a branch.
2. Do the work. Append decisions to the epoch record _as you make them_ —
   reconstructing a rationale three months later is the expensive part.
3. When an epoch closes: fill in Outcome, Deferred, and Gotchas; flip the
   status here and in the record's own header.
4. Production workloads move by a pull request changing
   `clusters/management/releases.yaml`, not by a tag (#510).

Start a fresh session per epoch rather than one long chat. `CLAUDE.md` plus
this log is enough to bring a cold session fully up to speed.

New epochs: copy [`00-template.md`](00-template.md).
