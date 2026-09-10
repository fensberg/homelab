# =============================================================================
# Object storage bucket. Vendor: Cloudflare R2 (cloudflare/cloudflare provider).
#
# Two different things back up here, at two different layers:
#
#   postgres/    CloudNativePG's WAL archive and base backups, written by the
#                database itself. This is what routine point-in-time recovery
#                restores from.
#
#   state/       An age-encrypted dump of the OpenTofu state, written by the
#                Backup phase of the start button.
#
# The second one exists because the first is not sufficient on its own. The
# database backups restore into a running cluster - but rebuilding the cluster
# needs the state that lives in that database. The standalone state dump is
# what breaks that circle after a total loss.
#
# Nothing reaches this bucket in plaintext: Postgres backups are written by a
# database whose credentials never leave the cluster, and the state dump is
# age-encrypted to a public recipient before upload.
# =============================================================================

# Orphan adoption happens in Go (cluster.go), before this gets applied - see
# compute.tf's talos_disk_image comment for why a static `import` block here
# is the wrong tool: it always attempts the read and hard-fails when the
# bucket genuinely does not exist yet, which is the normal, common case.
resource "cloudflare_r2_bucket" "homelab" {
  account_id = local.object_storage_account.account_id
  name       = local.object_storage.bucket

  # No location argument, deliberately. It is schema'd Optional+Computed
  # with RequiresReplace - asserting a value here holds Cloudflare to it as
  # a firm promise, but Cloudflare's own R2 docs describe the location hint
  # as best-effort, not guaranteed. Setting it to "WNAM" got "ENAM" back
  # from Create every time (twice, identically - not random flakiness, more
  # likely this account's data-location policy overriding the hint), which
  # OpenTofu's provider SDK then reports as "Provider produced inconsistent
  # result after apply" and fails the whole apply. Leaving it unset lets
  # Cloudflare assign whatever it was always going to assign, with nothing
  # asserted here for it to disagree with.
}

# The workload bucket, and its whole point is that it outlives this estate.
#
# The bucket above holds backups OF this estate, so the teardown empties it
# deliberately - they describe something that is about to stop existing. This
# one holds backups of what RUNS on the estate, which is the opposite: a world
# save, or anything else a workload accumulates that people would mind losing.
#
# A cluster rebuild is the normal way a Talos version reaches these machines
# (#97), and a rebuild is a demolish followed by an ignition. So "survives a
# teardown" is not an edge case here, it is the routine path - see #330 and the
# storage section of docs/epochs/03-workload.md.
#
# The name is derived rather than configured. It needs no vault item of its
# own, cannot drift from the bucket it sits beside, and keeps a real name out
# of this file.
#
# Sterilize forgets this resource before the destroy runs, and the next
# ignition adopts it back. That is deliberate and it is the exception to the
# rule stated in teardown.go - that forgetting something which outlives the VMs
# leaves a real thing nothing tracks. It does, for exactly as long as there is
# no estate to track it, and adoption is what closes that window.
resource "cloudflare_r2_bucket" "workloads" {
  account_id = local.object_storage_account.account_id
  name       = "${local.object_storage.bucket}-workloads"

  # No location, same reason as above.
}

output "workload_storage_bucket" {
  value = cloudflare_r2_bucket.workloads.name
}

output "object_storage_bucket" {
  value = cloudflare_r2_bucket.homelab.name
}
