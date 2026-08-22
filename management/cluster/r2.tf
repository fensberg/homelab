# =============================================================================
# Cloudflare R2 bucket for off-site OpenTofu state backups.
#
# The state file ends up living in a Postgres that runs ON the cluster the
# state describes. Lose the cluster, lose the state. This bucket is the way
# out of that circular dependency.
#
# What lands here is always age-encrypted before upload (see the Backup phase
# in scripts/Start-Homelab.ps1). R2 never sees plaintext state, and the
# private identity needed to decrypt it lives only in 1Password.
# =============================================================================

resource "cloudflare_r2_bucket" "state_backup" {
  account_id = local.config.state.r2.account_id
  name       = local.config.state.r2.bucket

  # Western North America. Change to match where you actually are; R2 egress
  # is free, so this is about latency, not cost.
  location = "WNAM"
}

output "r2_state_bucket" {
  value = cloudflare_r2_bucket.state_backup.name
}
