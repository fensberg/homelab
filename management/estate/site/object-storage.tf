# The site's buckets, and a key for each that reaches that bucket and nothing
# else.
#
# R2 scopes a token per bucket, and a token that may write may also delete, so
# a bucket is exactly one blast radius. That gives the rule every entry follows:
# two things belong in different buckets if and only if one being compromised
# must not be able to destroy the other.
#
#   database     the state database's WAL archive and base backups, written
#                continuously from inside the cluster. A site teardown empties
#                it with this key; nothing else reaches it.
#   state        age-encrypted OpenTofu state dumps. The break-glass copy, so
#                apart from the database whose loss it exists to survive.
#   staging      staging workload data.
#   production   production workload data.
#
# Named <estate>-<site>-<purpose>. The estate leads because the estate made
# them, so anything in the account without that prefix was made by hand. R2
# cannot nest buckets and its dashboard sorts by name, so this is the only
# grouping it offers: estate, then site, then purpose.
#
# Every bucket outlives the estate's machinery. demolish-estate releases them
# from state rather than destroying them, and build-estate adopts any that
# already exist, because the world backups in production are the only copy of
# the world while no site stands.
locals {
  purposes = toset(["database", "state", "staging", "production"])
}

resource "cloudflare_r2_bucket" "this" {
  for_each   = local.purposes
  account_id = var.account_id
  name       = "${var.prefix}-${each.key}"
}

# One key per bucket, owned by the account rather than by any person, so it
# does not vanish with a user and appears in the account's own token list.
# R2 reads an API token as an S3 key pair: the access key id is the token's
# id, and the secret is the SHA-256 of its value.
resource "cloudflare_account_token" "this" {
  for_each   = local.purposes
  account_id = var.account_id
  name       = "${var.site} ${each.key} bucket"

  policies = [{
    effect            = "allow"
    permission_groups = [{ id = var.r2_bucket_write }]
    resources = jsonencode({
      "com.cloudflare.edge.r2.bucket.${var.account_id}_default_${cloudflare_r2_bucket.this[each.key].name}" = "*"
    })
  }]
}
