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

  location = "WNAM"

  lifecycle {
    # location is a placement hint, not a guarantee: Cloudflare's own API
    # has read the bucket back as a different region code than requested
    # (WNAM requested, ENAM read back), which OpenTofu treats as drift to
    # correct - but the provider's own response to that "fix" attempt then
    # disagrees with its own plan, a documented "bug in the provider"
    # (its error text says so) rather than anything in our config. Ignoring
    # it stops that from turning into a failed apply on every run that
    # happens to re-touch this resource.
    ignore_changes = [location]
  }
}

output "object_storage_bucket" {
  value = cloudflare_r2_bucket.homelab.name
}
