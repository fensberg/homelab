# =============================================================================
# Object storage. Vendor: Cloudflare R2 (cloudflare/cloudflare provider).
#
# FOUR BUCKETS, AND THE RULE THAT PRODUCED THEM.
#
# R2 API tokens scope per BUCKET. There is no prefix or directory condition on
# a permanent token, and a token permitted to write is also permitted to delete
# (#94). So a bucket is exactly one blast radius, and a prefix inside one buys
# no protection whatsoever - it is filing, not isolation.
#
# That gives one rule, and every entry below follows from it: two things belong
# in different buckets if and only if one being compromised must not be able to
# destroy the other.
#
#   <site>              the state database's WAL archive and base backups,
#                       written continuously by CloudNativePG from inside the
#                       cluster. Destroyed with the estate.
#
#   <site>-state        age-encrypted OpenTofu state dumps, written by the
#                       Backup phase. Outlives the estate.
#
#   <site>-staging      staging workload data.    Outlives the estate.
#   <site>-production   production workload data. Outlives the estate.
#
# NAMED FOR THE SITE, BECAUSE THE SITE IS THE ISOLATION BOUNDARY. `<site>` is
# `local.site_name` - the slug of the site's vault name, the same value that
# names every VM, so a site's buckets read `<site>-state` beside machines called
# `<site>-cp-100`. Every site in an estate shares one R2 account, so two sites
# whose buckets collide are two sites writing into each other's state dumps.
#
# The estate is NOT in the name. Every bucket in the account belongs to the
# estate, so a prefix saying so distinguishes nothing.
#
# The real name reaches R2 and the rendered config and never git, which is the
# line this repository draws everywhere: obfuscate in public, use real names
# where it is private and a reader benefits.
#
# WHY THE STATE DUMPS ARE NOT IN THE DATABASE BUCKET, which is where they were.
# The state dump exists precisely because the database backups are not
# sufficient on their own: they restore into a running cluster, and rebuilding
# that cluster needs the state held in that database. The dump is what breaks
# the circle after a total loss. While both sat in one bucket, the credential
# that lives permanently in a cluster Secret could delete the break-glass copy
# along with the backups it was meant to rescue - one compromise taking both
# layers of a deliberately two-layer design.
#
# WHY STAGING AND PRODUCTION ARE BUCKETS AND NOT PREFIXES. Staging is where the
# less-trusted things run, so a staging compromise must not reach production's
# data. By the rule above that makes it a bucket boundary.
#
# AND WHY THERE IS NO -state-staging. CLAUDE.md: the management tier has no
# staging or production form - one cluster hosts both overlays, separated by
# namespace. Environment is a property of a WORKLOAD's data and of nothing
# else. A platform bucket carrying an environment suffix would be asserting a
# second platform that does not exist.
#
# Nothing reaches any of these in plaintext: Postgres backups are written by a
# database whose credentials never leave the cluster, and the state dump is
# age-encrypted to a public recipient before upload.
#
# =============================================================================

# The set is created here as four named resources and declared again in
# scripts/contractor/internal/config/buckets.go, which decides what happens to
# each one at teardown. Twice on purpose - the same defence in depth the config
# contract uses, because this side creates them and that side decides their
# fate. TestTheBucketTableAgreesWithTheHCL refuses any drift between the two.
#
# FOUR NAMED RESOURCES RATHER THAN ONE `for_each`. The set is fixed and small,
# and a for_each keys every address by a map key - which
# TestResourceAddressKeysUseAPlaceholder refuses, because for_each keys
# normally come from the config and a name in an address is a vault value
# published in a plan. These keys would have been literals rather than config
# values, but a guard that has to distinguish those two is a guard with an
# exception in it, and the estate's rule is to move the work to where the guard
# already looks. Named resources also give `tofu state rm` an address with no
# quoting in it, which is the command that decides whether a bucket survives a
# teardown.
#
# Orphan adoption happens in Go (cluster.go), before any of this is applied -
# see compute.tf's talos_disk_image comment for why a static `import` block is
# the wrong tool: it always attempts the read and hard-fails when the bucket
# genuinely does not exist yet, which is the normal, common case.

# The state database's WAL archive and base backups. Destroyed with the estate:
# they describe something that is about to stop existing.
#
# Keeps the site's configured name with no suffix, and that is load-bearing
# rather than lazy. CloudNativePG's destinationPath points at this bucket, and
# renaming it starts a fresh WAL archive with no base backup behind it - a live
# migration nobody should be pushed into by a tidy-up. The rename becomes free
# at the next rebuild, which destroys and recreates this bucket anyway.
resource "cloudflare_r2_bucket" "database" {
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

# The age-encrypted OpenTofu state dumps. Outlives the estate.
#
# Separate from the database bucket above, which is the entire point. This is
# the copy that exists to survive the database being lost - it is what breaks
# the circle when rebuilding a cluster needs the state held inside that
# cluster. While the two shared a bucket they shared a credential, so the key
# sitting permanently in a cluster Secret could delete the break-glass copy
# along with the backups it was meant to rescue.
#
# It also stops demolish destroying them (#94): the teardown empties the bucket
# it is about to delete, so the operation most likely to precede needing a
# state dump was the operation that destroyed every one of them.
resource "cloudflare_r2_bucket" "state" {
  account_id = local.object_storage_account.account_id
  name       = "${local.site_name}-state"

  # No location, same reason as above.
}

# Staging workload data. Outlives the estate.
resource "cloudflare_r2_bucket" "staging" {
  account_id = local.object_storage_account.account_id
  name       = "${local.site_name}-staging"

  # No location, same reason as above.
}

# Production workload data. Outlives the estate.
#
# A cluster rebuild is the routine way a new Talos version reaches these
# machines (#97), and a rebuild is a demolish followed by an ignition - so
# "survives a teardown" is the normal path here rather than an edge case. See
# #330 and the storage section of docs/epochs/03-workload.md.
resource "cloudflare_r2_bucket" "production" {
  account_id = local.object_storage_account.account_id
  name       = "${local.site_name}-production"

  # No location, same reason as above.
}

# The database bucket already exists under a resource name that said nothing
# about what was in it. Renaming the RESOURCE is free; renaming the BUCKET is
# not, for the reason above, so only the address moves.
#
# It is also the one bucket still named from `object_storage.bucket` rather than
# from the site, and it cannot move to the site name in place: OpenTofu would
# have to destroy and recreate it, and Cloudflare refuses to delete a bucket
# holding objects - which this one does, continuously. The next rebuild
# recreates it anyway, so the rename is free then and impossible now. At that
# point the `object_storage.bucket` config key goes with it.
moved {
  from = cloudflare_r2_bucket.homelab
  to   = cloudflare_r2_bucket.database
}

# `cloudflare_r2_bucket.workloads` is deliberately NOT moved here, and its
# absence is the whole transition.
#
# It held `<name>-workloads`, which the staging and production buckets replace.
# A bucket cannot be renamed in R2, so this is a destroy and a create rather
# than a move - and it is safe only because nothing has ever written to it: the
# world backup that was meant to fill it does not exist yet (#372), which is
# also why this is the cheapest possible moment to make the change.
#
# If that turns out to be wrong, the failure is the safe one. Cloudflare
# refuses to delete a bucket with objects in it, so the apply stops and says so
# rather than quietly taking something with it.

output "object_storage_buckets" {
  description = "Every bucket the estate holds."
  value = {
    database   = cloudflare_r2_bucket.database.name
    state      = cloudflare_r2_bucket.state.name
    staging    = cloudflare_r2_bucket.staging.name
    production = cloudflare_r2_bucket.production.name
  }
}
