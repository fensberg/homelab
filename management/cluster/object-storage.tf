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

output "object_storage_bucket" {
  value = cloudflare_r2_bucket.homelab.name
}
