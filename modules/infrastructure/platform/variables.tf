# =============================================================================
# The platform: what is put on a cluster that already stands.
#
# A site is two roots. The cluster's builds the machines and ends at "the
# nodes are Ready"; the platform's starts there, and creates the namespaces
# and secrets Flux cannot, then starts Flux. They are separate so that no
# provider is configured from a resource in its own root: the kubernetes
# provider is configured by the root that calls this module, from the
# cluster's access handed to it as a value already applied
# (docs/epochs/02-abstraction.md, "Inside a site, the split is two roots
# sharing one config").
#
# Both modules read the same rendered config, so nothing is plumbed between
# them but that access.
# =============================================================================

variable "site" {
  type        = string
  default     = "site0"
  description = <<-EOT
    Which key in the config's sites map to deploy, e.g. "site0". Selection
    only - the site's identity comes from the octet it declares. Set by the
    start button via TF_VAR_site.
  EOT
  # Checked here rather than as a precondition on terraform_data.invariants,
  # where it used to live and could never actually fire: local.site indexes
  # sites[var.site] directly, so a mistyped -site failed on a raw "Invalid
  # index" against this file's own line 25 long before any precondition was
  # evaluated. A variable validation runs before locals are evaluated at all,
  # and - unlike a resource precondition - cannot be targeted away by the
  # -target'd applies the Compute phase issues.
  #
  # Referencing another variable from a validation needs OpenTofu >= 1.9,
  # which is why versions.tf's required_version is no longer >= 1.6.0.
  validation {
    condition     = contains(keys(jsondecode(file(var.config_path)).sites), var.site)
    error_message = "Unknown site '${var.site}'. The config at ${var.config_path} defines: ${join(", ", sort(keys(jsondecode(file(var.config_path)).sites)))}."
  }
}

variable "config_path" {
  type        = string
  default     = "../../config/management.rendered.json"
  description = <<-EOT
    Path to the rendered config JSON. Overridden only by tofu test, to point
    at a fixture instead of the real rendered config - a real run, task
    validate's placeholder render, and CI never set this, so the default is
    the only path any of them ever see.
  EOT
}

variable "kubeconfig" {
  type        = string
  sensitive   = true
  description = <<-EOT
    The same access as a kubeconfig, the cluster root's output of the same
    name. Only for kubectl in the Flux bootstrap, which cannot authenticate
    from values held in memory.
  EOT
}

variable "applications_path" {
  type        = string
  default     = null
  description = <<-EOT
    Where the applications are, each declaring the Secrets and routes it
    needs. Unset outside this module's tests: a site reads those beside the
    module, which are those of the commit the site is pinned to.
  EOT
}

variable "site_directory" {
  type        = bool
  description = <<-EOT
    Whether the repository has a directory of this site's own in the Flux
    tree, which is how a site is given work. With one, the site's Flux
    reconciles that directory, which builds on the core; with none it
    reconciles the core alone. The root says, because the root reads the
    repository as it is, and this module is read as it was at the site's pin.
  EOT
}

locals {
  config = jsondecode(file(var.config_path))
  site   = local.config.sites[var.site]

  # This site's slice of the address plan, called in address-plan.tf exactly
  # as the cluster root calls it.
  net      = module.address_plan.sites[var.site]
  cp_keys  = sort(keys(local.net.control_planes))
  node_ips = [for k in local.cp_keys : local.net.control_planes[k].ip]

  object_storage = local.site.object_storage

  # The account's S3 API address, which every bucket in it shares. One
  # expression for the database's backups and every application's. The account and the
  # buckets are granted by the estate; the site creates neither.
  object_storage_endpoint = "https://${local.object_storage.account_id}.r2.cloudflarestorage.com"
  site_database           = local.site.database


  # --- CI runners ----------------------------------------------------------
  # Fleet plane, like the object storage account: one GitHub App serves the
  # estate, and a runner is a site-level deployment of an estate-level
  # identity. See runner.tf for why the App is scoped the way it is.
  runner = local.config.source_control.foreman_bot

  # Only what OpenTofu itself creates. The scale set's own name, its runner
  # group and its ceiling are declared in the manifest that uses them, because
  # nothing here reads them - a local kept alive purely so a test could compare
  # against it is dead code with a test-shaped excuse, and tflint was right to
  # say so.
  runner_system_namespace = "arc-systems"
  runners_namespace       = "arc-runners"
  runner_secret_name      = "github-app-credentials"

  # The scale set is registered against the organization, not the repository,
  # because that is the scope the App was granted. Derived from the repository
  # URL rather than declared again, so the two cannot disagree:
  # https://host/org/repo -> https://host/org.
  runner_github_config_url = join("/", slice(split("/", local.config.source_control.repo_url), 0, 4))

  # --- tunnel --------------------------------------------------------------
  # Required rather than defaulted, unlike alerting: its credential is held to
  # the vendor attestation in registry.tf, and a default would be a way for
  # that check to pass on a value nobody attested.
  tunnel = local.config.tunnel

  # --- alerting ------------------------------------------------------------
  #
  # Where the estate speaks when something it monitors goes wrong. Fleet-level
  # for the same reason workloads are: one person reads it, and a second site
  # would report into the same place rather than somewhere new.
  #
  # The webhook URL is the whole credential - an incoming webhook authenticates
  # by being known - so it is written into a Secret the monitoring namespace
  # reads and never into a manifest in git.
  alerting = try(local.config.alerting, {
    provider    = ""
    webhook_url = ""
  })

  # --- what the site's Flux reconciles --------------------------------------
  # The core is what every site runs, and holds Flux's own install. A site
  # that has been given work has a directory named for it beside the core.
  flux_core   = "clusters/core"
  gitops_path = var.site_directory ? "clusters/${var.site}" : local.flux_core

  # --- state database ------------------------------------------------------
  state_db_namespace = "database"
  state_db_cluster   = "tofu-state"
  state_db_name      = "tofu_state"
  state_db_owner     = "tofu"
  state_db_nodeport  = local.net.state_database.port
}
