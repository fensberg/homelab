# What the estate reads of its vendors when it is planned: the zone and the
# token permissions it grants from at Cloudflare, and its organisation and
# repository at GitHub. Read once, here, so every use reads local.read.
#
# Planned offline (var.offline), nothing is read and each is a stand-in of
# the shape the vendor answers with. The rule harness plans the estate that
# way, from nothing and with no credential, to judge what it would build; a
# converge never does, and reads them all.
locals {
  read = var.offline ? {
    zone                            = "offline-zone"
    zone_read                       = "offline-zone-read"
    dns_write                       = "offline-dns-write"
    r2_bucket_write                 = "offline-r2-bucket-write"
    r2_bucket_read                  = "offline-r2-bucket-read"
    default_branch                  = "main"
    repo_id                         = 0
    collaborators                   = 0
    two_factor_is_required          = true
    members_may_create_repositories = false
    } : {
    zone                            = data.cloudflare_zone.estate[0].zone_id
    zone_read                       = one(data.cloudflare_account_api_token_permission_groups_list.zone_read[0].result).id
    dns_write                       = one(data.cloudflare_account_api_token_permission_groups_list.dns_write[0].result).id
    r2_bucket_write                 = one(data.cloudflare_account_api_token_permission_groups_list.r2_bucket_write[0].result).id
    r2_bucket_read                  = one(data.cloudflare_account_api_token_permission_groups_list.r2_bucket_read[0].result).id
    default_branch                  = data.github_repository.estate[0].default_branch
    repo_id                         = data.github_repository.estate[0].repo_id
    collaborators                   = length(data.github_collaborators.estate[0].collaborator)
    two_factor_is_required          = data.github_organization.estate[0].two_factor_requirement_enabled
    members_may_create_repositories = data.github_organization.estate[0].members_can_create_repositories
  }
}
