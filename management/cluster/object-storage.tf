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

resource "cloudflare_r2_bucket" "homelab" {
  account_id = local.object_storage.account_id
  name       = local.object_storage.bucket

  # Western North America. Change to match where you actually are; R2 egress
  # is free, so this is about latency, not cost.
  location = "WNAM"
}

output "object_storage_bucket" {
  value = cloudflare_r2_bucket.homelab.name
}
