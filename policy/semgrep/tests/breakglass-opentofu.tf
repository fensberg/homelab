locals {
  # ok: the-break-glass-identity-is-never-referenced-by-opentofu
  # never read local.config.state_backup.identity here
  # ruleid: the-break-glass-identity-is-never-referenced-by-opentofu
  identity = local.config.state_backup.identity
  # ruleid: the-break-glass-identity-is-never-referenced-by-opentofu
  ref = "op://estate/state_backup/identity"
  # ruleid: the-break-glass-identity-is-never-referenced-by-opentofu
  old = var.site.backup_identity
  # ok: the-break-glass-identity-is-never-referenced-by-opentofu
  recipient = local.config.state_backup.recipient
}
