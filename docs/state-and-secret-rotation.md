# State and secret rotation

The workspace is sterilized on every exit — rendered secrets and local state
are deleted whether a run succeeded or failed. That is necessary and it is not
sufficient.

**Deletion is not the security property. Being worthless is.** Deleting a file
assumes the delete happened, that nothing copied it first, and that no backup,
crash dump, snapshot or editor swap file caught it in between. The stronger
design is that whatever was on local disk stops being usable against the
cluster the moment it is no longer needed, so a leaked copy buys an attacker
nothing.

This document is what that means in practice here.

## What a leaked state file actually exposes

OpenTofu state stores resource attributes verbatim, including the sensitive
ones. `terraform.tfstate` for this project holds:

| From                                           | What is in there                                                            | Worst case if leaked                                                                   |
| ---------------------------------------------- | --------------------------------------------------------------------------- | -------------------------------------------------------------------------------------- |
| `talos_machine_secrets`                        | The entire Talos PKI — cluster CA private key, etcd CA, service-account key | **Total cluster compromise.** Mint credentials for any node or user, forever           |
| `talos_cluster_kubeconfig`                     | Kubernetes client certificate and key                                       | `cluster-admin` until the CA is rotated                                                |
| `kubernetes_secret.state_db_credentials`       | The state database password                                                 | Read and write the state itself                                                        |
| `kubernetes_secret.object_storage_credentials` | R2 access key and secret                                                    | **A second door into the same room** — see below, not merely "read and delete backups" |
| `tailscale_tailnet_key`                        | The hypervisor's tailnet auth key                                           | Join the overlay network as a node                                                     |

The Talos PKI is the one that matters most and is the hardest to rotate. Rank
everything below against that.

## The off-site copy is two different things

It is tempting to think of the R2 bucket as "the encrypted fallback". One
_prefix_ is. The bucket is not.

| Prefix                | Written by                  | Protection                                                                                                                                                 |
| --------------------- | --------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `management-cluster/` | ignite's Backup phase       | age-encrypted. The private identity is deliberately absent from config, state and the cluster — the automation can write backups and cannot read them back |
| `postgres/`           | CloudNativePG, continuously | `compression: gzip`. That is the whole of it                                                                                                               |

The second row is the problem, because **the Postgres database _is_ the
OpenTofu state.** So `postgres/` is a continuously-refreshed, readable copy of
everything the age file protects: the Talos PKI, the state database password,
and the R2 credentials themselves.

That closes a loop worth drawing explicitly:

```text
leaked state file  ->  R2 credentials  ->  read postgres/  ->  the whole PKI
```

The age encryption does not stand in the way, because an attacker never has to
touch the age file. They read the other prefix. One set of credentials covers
both — CloudNativePG and the Backup phase authenticate with the same key.

**This is the real argument for Layer 1, and it is stronger than "it protects
the local file".** Encrypt the state and the ciphertext flows all the way
down: the rows in Postgres are ciphertext, so the WAL archive is ciphertext,
so the base backups are ciphertext, and the age dump is encrypted twice. One
change makes the whole chain worthless without the passphrase in 1Password.

One thing not to do: barman supports `encryption: AES256` on the object store,
and it looks like the obvious fix. It is not. That is server-side encryption,
where Cloudflare holds the key — it defends against someone stealing disks in
a datacentre, not against someone holding your R2 credentials, which is the
threat that actually exists here.

## Layer 1 — encrypt the state, so the file is worthless by construction

OpenTofu has native state encryption (confirmed working on the pinned 1.12.6:
the state file on disk contains an `encrypted_data` blob and no plaintext).
This is the strongest available answer, because it does not depend on cleanup
running or on rotation being remembered — the bytes are unreadable the moment
they are written.

**This is built and on.** `scripts/ignite/internal/phases/encryption.go`
resolves the passphrase from 1Password — generating one on first use, the same
way the state database password is generated — and sets `TF_ENCRYPTION` before
any phase runs, `-destroy` included.

**Design.** The entire `encryption` block is supplied through the
`TF_ENCRYPTION` environment variable rather than committed to `.tf` files.
Nothing in git then reveals the scheme or the key, and a bare `tofu` run
without that variable cannot read state at all — which is the lock, not a
side effect. `tofu validate`, `tofu test` and every CI lane are unaffected,
because none of them touch state.

There is no fallback method and no migration mode in the program. A fresh
estate is encrypted from its first apply and has no unencrypted state to fall
back to, and a fallback left switched on is precisely what keeps unencrypted
state readable. If ignite finds `TF_ENCRYPTION` already set it leaves it
alone, which is what makes the manual cutover below possible without fighting
it.

```hcl
# The shape of what TF_ENCRYPTION carries. The passphrase comes from
# op://homelab/<site>/database/encryption_passphrase.
key_provider "pbkdf2" "primary" {
  passphrase = "<from 1Password>"
}
method "aes_gcm" "primary" {
  keys = key_provider.pbkdf2.primary
}
state {
  method = method.aes_gcm.primary
}
```

> **Migrating an estate that already has unencrypted state is a deliberate
> one-time procedure, run by hand.** It is not something ignite offers, because
> it would be a code path used once per estate that weakens the property for as
> long as it stays switched on. Export `TF_ENCRYPTION` yourself with the block
> below and ignite will leave it alone.
>
> The sequence below has been **rehearsed for real** — as a hermetic test in
> `tests/go/encryption`, and by hand once against a throwaway Postgres in
> Docker using the same `pg` backend the live state uses. In that rehearsal the
> database row went from readable JSON to an encrypted blob, and back to
> readable through `tofu state pull`.
>
> ```hcl
> # Step 1. Note `method "unencrypted" "migrate"`. An empty `fallback {}` does
> # NOT parse - OpenTofu rejects it as an invalid expression - and the fallback
> # must name an explicit unencrypted method. Found by running it.
> terraform {
>   encryption {
>     method "unencrypted" "migrate" {}
>     key_provider "pbkdf2" "primary" {
>       passphrase = "<from 1Password>"
>     }
>     method "aes_gcm" "primary" {
>       keys = key_provider.pbkdf2.primary
>     }
>     state {
>       method = method.aes_gcm.primary
>       fallback {
>         method = method.unencrypted.migrate
>       }
>     }
>   }
> }
> ```
>
> 1. Apply the config above, then run any state-writing command —
>    `tofu apply -refresh-only` is enough. State is re-encrypted in place.
> 2. Confirm: the state is now ciphertext at rest, and `tofu state pull` still
>    returns usable JSON (which is what the Backup phase pipes into age).
> 3. Remove the `fallback` block **and** the `method "unencrypted" "migrate"`
>    declaration. Unencrypted state is now refused outright. Verified: putting
>    an old unencrypted state file back produces the error
>    `encountered unencrypted payload without unencrypted method configured`.
>
> Do this with a fresh `task backup-state` in hand, and confirm the age backup
> restores before starting.

## Layer 2 — rotation, so what _was_ on disk goes stale

Encryption protects the file. Rotation protects against the case where the key
leaked too, or where state was read while decrypted. Ordered by value to an
attacker.

### Object storage keys — easy, and more urgent than it looks

Easy because R2 API tokens are created and revoked through the Cloudflare API
with no downtime, and the credential in the cluster is a Kubernetes secret
CloudNativePG re-reads. Urgent because of the loop above: these keys read
`postgres/`, and `postgres/` is the state. **Treat a leaked state file as a
leaked PKI even if the age dump was never touched.**

Rotating by hand is: create a new token scoped to the one bucket with Object
Read & Write, update
`op://homelab/<site>/object_storage/{access_key_id,secret_access_key}`,
re-run ignite's Cluster phase to rewrite the secret, confirm WAL archiving
still works, then delete the old token.

**Automating it is blocked on one console change, and this was checked rather
than assumed.** These keys end up as attributes of
`kubernetes_secret.object_storage_credentials`, which puts them in state — so
by this project's own rule (a secret that lands in state is ours to generate)
they belong on the generated side, alongside the database password, minted
fresh every run. Cloudflare supports exactly that: an R2 S3 credential is an
account API token, where `access_key_id` is the token's id and
`secret_access_key` is the SHA-256 of its value, and
`cloudflare_api_token` is a resource the pinned provider already has.

What stops it today is scope. Asked directly, the admin token in
`op://homelab/site0/object_storage/admin_token` answers:

| Request                                       | Result                                   |
| --------------------------------------------- | ---------------------------------------- |
| `GET /accounts/{id}/r2/buckets`               | `200` — it manages buckets, as it should |
| `GET /accounts/{id}/tokens/permission_groups` | `403` code 9109, unauthorized            |

So it can create the bucket and cannot create the credential that reads it.
Widening it means adding **Account · API Tokens · Edit** to that token in the
Cloudflare console — a console action, which puts it on the same honest floor
as every other credential a third party issues. Until that happens, generating
these per run would be code that cannot work, and building it untested is the
failure mode this project keeps re-learning.

### Proxmox API token — easy

`hypervisor-prep.yml` already generates a token and writes it to 1Password,
including the read-back verification. Deleting the token in Proxmox and
re-running the Hypervisor phase rotates it.

### State database password — moderate, and the one that can lock you out

The password appears in three places that must move together: the 1Password
item, the Kubernetes secret CloudNativePG reads, and the `conn_str` the
OpenTofu pg backend was initialised with. Rotate in that order, and keep a
shell open with the old connection string until the new one is proven, because
a half-rotated password means no access to the state that describes how to fix
it. Take a `task backup-state` first.

### Tailnet key — already ephemeral

The Overlay phase mints a tagged, short-lived auth key per run. A leaked one
from an old state file has almost certainly expired. Nothing to do routinely;
revoke in the admin console if a specific run is suspect.

## Layer 3 — Talos PKI

The crown jewel, and the only one with no cheap answer. `talosctl` has a
first-class command for it:

```sh
# --dry-run defaults to TRUE. Run it first and read the output.
talosctl rotate-ca --nodes <cp-ips> --control-plane-nodes <cp-ips>

# Then, for real:
talosctl rotate-ca --dry-run=false \
  --nodes <cp-ips> --control-plane-nodes <cp-ips> \
  --output ./talosconfig.new
```

It rotates the Talos API CA and the Kubernetes API server issuing CA,
generating new CAs and applying them gracefully. Four things it does **not**
do, which is where the work is:

1. **Other Kubernetes PKI is not covered.** Per the command's own help, the
   rest is rotated by applying machine config changes to the control-plane
   nodes.
2. **Every existing credential dies.** The old `talosconfig` and every
   `kubeconfig` stop working. Regenerate and redistribute them, including the
   one the OpenTofu `kubernetes` provider uses.
3. **OpenTofu state goes stale.** `talos_machine_secrets` is a managed
   resource; rotating out of band means state now describes secrets that no
   longer exist. Follow with a `tofu apply -refresh-only` and read the plan
   carefully before any subsequent apply — a plan that proposes to _recreate_
   the machine secrets is proposing to rebuild the cluster.
4. **It is not a fire drill you want to discover under pressure.** Rehearse it
   on a disposable site (`HOMELAB_TEST_SITE=site1`) before you need it.

Because of (3), the honest sequencing for a suspected state leak is: rotate
everything in Layer 2 first, then decide whether the Talos PKI rotation is
warranted, and if it is, treat rebuilding the cluster from scratch as a
serious alternative. `ignite -destroy` followed by a fresh ignition is well
tested, fully automated, and takes less time than a careful CA rotation — and
it leaves nothing behind to be uncertain about.

## What off-site recovery looks like here, and why

The threat model is deliberately scoped. The data in this estate is
reproducible by re-running ignition, so its value is the practice rather than
the contents. Controls justified mainly by protecting the _data_ lose to
controls that keep the _mechanism_ honest and the storage flat.

Three things were considered and deliberately rejected, so they do not get
re-proposed:

- **A second copy at a second vendor.** 3-2-1 is right when losing the data
  costs something. Here it costs a re-run.
- **Compliance-mode immutability.** R2 could not do it anyway — see below —
  and it defends against a compromised Cloudflare account, which is a scenario
  with much larger problems attached.
- **Bucket locks.** Verified against Cloudflare's docs and genuinely capable:
  R2 implements neither S3 Object Lock nor versioning (both listed
  unimplemented, including `x-amz-bucket-object-lock-enabled` at creation), but
  it has its own _bucket locks_, which "prevent the deletion and overwriting of
  objects... for a specified period — or indefinitely", can target a prefix,
  and take strict precedence over lifecycle rules. Rejected because they fight
  the thing that matters more here: a locked object refuses to be pruned, so
  bounded storage and immutability cannot both be had. Safety comes from the
  code instead, below.

### One key for work, one for breaking glass

There are two encryption keys and they do different jobs:

| Key                                 | Where it lives              | What it protects                                                                                         | Who can use it       |
| ----------------------------------- | --------------------------- | -------------------------------------------------------------------------------------------------------- | -------------------- |
| OpenTofu `TF_ENCRYPTION` passphrase | 1Password, read by ignite   | State at rest — the local file, the Postgres rows, and therefore the WAL and base backups in `postgres/` | The automation       |
| age identity                        | Offline, in a human's hands | The standalone dump in `management-cluster/`                                                             | **Nobody automatic** |

That is the whole custody story. Day to day there is one key. The age identity
is used for nothing routine and exists so that one artefact survives the
compromise of everything else — including the 1Password service account. It is
the break-glass, rather than a separate mechanism bolted on next to one.

The automation can _write_ the age dump and cannot read it back, which is why
the health check below verifies its shape and not its contents. That is a
deliberate limit, not an oversight.

### Storage stays flat, and never at the cost of the only copy

`management-cluster/` keeps `latest.tfstate.age` plus **one** previous
generation. Two objects, fixed, forever. `postgres/` keeps 7 days rather than
30 — still a real point-in-time-recovery window, at roughly a quarter of the
storage, for a database holding a few hundred kilobytes.

The prune obeys one rule, which is the rule that was asked for: **never delete
the old copy until the new one is confirmed to exist.** It re-lists the bucket
after uploading and refuses to delete anything at all unless the object it just
wrote is in that listing — because "rclone exited zero" is a claim about a
request, not about what is in the bucket. A prune that cannot confirm the new
upload warns and does nothing, leaving more copies than intended, which is the
correct direction to fail in.

A prune failure never fails the Backup phase either. The backup has already
succeeded by that point, and turning a tidiness problem into a failed ignition
run would be the wrong trade.

### The alarm

Nobody watches this, so the check has to be the thing that would notice.

Passively asking "how old is the newest backup?" would be meaningless, because
backups otherwise happen only during an ignition run — a month-old copy is both
normal and indistinguishable from a pipeline that broke three weeks ago. So the
nightly job **takes a backup first** and then asserts against what landed. That
exercises the entire path every night, and makes freshness a question worth
asking.

It checks four things:

1. `latest.tfstate.age` exists, is under a day old, and is larger than a
   plausible floor.
2. It begins with `age-encryption.org/v1` — proof of a well-formed encrypted
   file rather than a truncated upload. This is the most that can be checked
   without the identity, and the identity is offline by design.
3. The number of stored generations is still bounded, which is how a prune that
   silently stopped running would show up.
4. WAL archiving is actually working, read from `pg_stat_archiver` — plain
   Postgres, whose column names are stable in a way CloudNativePG's own status
   fields are not. A last-failure more recent than the last success means
   archiving is broken right now.

**The alert is the workflow failing**, which GitHub already emails about — one
fewer moving part than a monitoring stack. And the nightly backup is also the
repair: if last night's was missing or stale, tonight's replaces it, and if it
cannot, the run goes red.

### Restoring

```sh
./scripts/ignite/ignite -site site0 -restore
```

This used to be three commands printed at the end of a run, which is not a
restore — it is a recipe to retype correctly under the one circumstance where
nobody is calm. The command fetches the break-glass identity, decrypts the
latest backup, checks that what came back is state describing something, and
pushes it through the encrypted backend. It refuses outright if local state
already exists, because restoring over live state replaces the description of a
running estate with an older one while the estate itself does not change to
match.

It restores state and stops. What to do next — re-ignite, tear down, or just
look at how far reality has drifted — is a judgement call made with the facts
in front of you, not a step to be chained onto a recovery.

It is also the only thing in this program that reads the private half at all;
`tests/go/repo/breakglass_test.go` fails if any other file does.

**Rehearsing it needs no second estate.** Right after a successful ignition,
move the local state aside, restore, and compare — the workstation is the
disposable part, not the site.

## What is deliberately not automated

Layer 2 and Layer 3 are runbooks rather than code, for now. Automating a
rotation that can lock you out of your own state is a change that has to be
rehearsed before it is trusted — but rehearsed locally, against throwaway
scaffolding on the workstation, not against a second site. There will not be a
second site for years, and treating one as a prerequisite is how these runbooks
stayed runbooks.

What _is_ automated is the part that needs no rehearsal: the Backup phase
never writes plaintext state to disk. It pipes `tofu state pull` straight into
`age` and wipes the in-memory buffer afterwards, so the only file that exists
at any point is the ciphertext.
