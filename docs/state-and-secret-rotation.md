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

**Design.** The entire `encryption` block is supplied through the
`TF_ENCRYPTION` environment variable rather than committed to `.tf` files.
Nothing in git then reveals the scheme or the key, and a bare `tofu` run
without that variable cannot read state at all — which is the lock, not a
side effect. Ignite resolves the passphrase from 1Password and sets the
variable before invoking tofu; `tofu validate`, `tofu test` and every CI lane
are unaffected, because none of them touch state.

```hcl
# The shape of what TF_ENCRYPTION carries. The passphrase comes from
# op://homelab/<site>/state-database/encryption_passphrase.
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

> **This is a deliberate one-time cutover, not a flag to flip.** State that is
> already in Postgres is unencrypted. Turning encryption on without a
> `fallback` block makes existing state unreadable. The migration is:
>
> 1. Add `state { method = method.aes_gcm.primary; fallback {} }` — the empty
>    fallback means "read unencrypted, write encrypted".
> 2. Run any state-writing command (`tofu apply -refresh-only`). State is now
>    encrypted in place.
> 3. Remove the `fallback` block. Unencrypted state is now refused.
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

Rotating is: create a new token scoped to the one
bucket with Object Read & Write, update
`op://homelab/<site>/object_storage/{access_key_id,secret_access_key}`,
re-run ignite's Cluster phase to rewrite the secret, confirm WAL archiving
still works, then delete the old token.

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

## Making off-site recovery enterprise grade

Today the recovery story is: one bucket, one credential, two prefixes, 30 days
of retention, and a restore procedure nobody has run. That is a backup. It is
not yet a recovery capability. In rough order of value:

### 1. Encrypt the state (prerequisite, not an optional extra)

Everything else on this list assumes the off-site copy is not a plaintext copy
of the cluster's private keys. Until Layer 1 is cut over, it is. Do this first.

### 2. Bucket locks, on both prefixes

**Verified against Cloudflare's own documentation**, because the obvious
answer turns out to be wrong: R2 does **not** implement S3 Object Lock and
does **not** support bucket versioning. Both are listed as unimplemented,
including `x-amz-bucket-object-lock-enabled` at bucket creation.

What R2 has instead is its own _bucket locks_, and they are the right tool:

- They "prevent the deletion and overwriting of objects in an R2 bucket for a
  specified period — or indefinitely."
- Rules can target a prefix, so `postgres/` and `management-cluster/` can carry
  different retention.
- They take **strict precedence over lifecycle rules**: "if a lifecycle rule
  attempts to delete an object at 30 days but a bucket lock rule requires it be
  retained for 90 days, the object will not be deleted until the 90-day
  requirement is met."

This is the control that turns "we have backups" into "we have backups an
attacker cannot take away", because the credential that writes the backups
cannot destroy them. It fits the credential split this project already makes:
the cluster holds only an Object Read & Write key, while the account-scoped
`admin_token` that could change a lock rule is wiped after ignition and lives
only in 1Password.

Two caveats to design around rather than discover:

- **Not compliance mode.** Lock rules can be removed or modified by the account
  owner. This defends against a compromised _cluster_, not a compromised
  _Cloudflare account_. See item 5.
- **It will break CloudNativePG's pruning.** `retentionPolicy: "30d"` issues
  deletes. Any object still inside a longer lock period will refuse to be
  deleted, and CNPG will log failures. Either align the lock period with the
  retention policy, or set the lock deliberately longer and accept the noise —
  but decide, because the default is a confusing error every night.

### 3. Split the credential by prefix

One key currently does everything. CloudNativePG needs write to `postgres/`
and nothing else; the Backup phase needs write to `management-cluster/` and
nothing else; neither needs to read the other's prefix, and neither needs
delete once bucket locks are in place. Two scoped tokens cost nothing and mean
a compromised cluster cannot even read the age dumps it did not write.

### 4. Restore on a schedule — the biggest gap

**A backup nobody has restored is a hypothesis.** Nothing here has ever been
restored, so the honest status of the recovery path is "unknown".

This is the item the new test framework is built for, and the one that most
separates a homelab from a production estate. A scheduled job on the
self-hosted runner that pulls the latest age dump and the latest CNPG base
backup, restores them into a throwaway target, asserts the state parses and
the database answers, then tears it down — that is an `integration` tier test
with a `restore` build tag, and it converts a hypothesis into a fact every
night. It needs the disposable site that `docs/ideas.md` already wants.

### 5. A second copy, at a second vendor

Bucket locks are removable by the account owner, so one compromised Cloudflare
login — or a billing lapse, or an account suspension — still takes everything.
The 3-2-1 answer is a second target in a different account at a different
provider, ideally one that _does_ implement S3 Object Lock in compliance mode
(AWS S3, Backblaze B2, Wasabi), where retention cannot be shortened by anyone,
including the account owner. The age dump is small and already encrypted, so
mirroring `management-cluster/` there is cheap; the CNPG backups are the
expensive half and can stay single-vendor if cost matters.

### 6. Custody for the age identity

The current instruction is "store the private key file somewhere offline". The
bus factor on the entire off-site recovery path is therefore one person and
one file. Split it (Shamir), or put it on two hardware tokens held by two
people, or escrow it — but write down who can actually perform a restore at
3am, because right now the answer is "one person, if they still have the file".

### 7. Notice when it stops

Nothing currently alerts if WAL archiving fails. CloudNativePG exposes the age
of the last archived WAL; a backup that quietly stopped six weeks ago is
indistinguishable from a healthy one until the day it matters.

## What is deliberately not automated

Layer 2 and Layer 3 are runbooks rather than code, for now. Automating a
rotation that can lock you out of your own state is a change that has to be
rehearsed against a disposable estate before it is trusted, and there is not
one yet — that is the "lower-tier environment" idea in `docs/ideas.md`, and it
is a prerequisite rather than a nice-to-have.

What _is_ automated is the part that needs no rehearsal: the Backup phase
never writes plaintext state to disk. It pipes `tofu state pull` straight into
`age` and wipes the in-memory buffer afterwards, so the only file that exists
at any point is the ciphertext.
