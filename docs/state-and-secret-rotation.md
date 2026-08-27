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

| From                                           | What is in there                                                            | Worst case if leaked                                                         |
| ---------------------------------------------- | --------------------------------------------------------------------------- | ---------------------------------------------------------------------------- |
| `talos_machine_secrets`                        | The entire Talos PKI — cluster CA private key, etcd CA, service-account key | **Total cluster compromise.** Mint credentials for any node or user, forever |
| `talos_cluster_kubeconfig`                     | Kubernetes client certificate and key                                       | `cluster-admin` until the CA is rotated                                      |
| `kubernetes_secret.state_db_credentials`       | The state database password                                                 | Read and write the state itself                                              |
| `kubernetes_secret.object_storage_credentials` | R2 access key and secret                                                    | Read and delete every off-site backup                                        |
| `tailscale_tailnet_key`                        | The hypervisor's tailnet auth key                                           | Join the overlay network as a node                                           |

The Talos PKI is the one that matters most and is the hardest to rotate. Rank
everything below against that.

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

### Object storage keys — easy, do this first

R2 API tokens are created and revoked through the Cloudflare API with no
downtime, and the credential in the cluster is a Kubernetes secret
CloudNativePG re-reads. Rotating is: create a new token scoped to the one
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
