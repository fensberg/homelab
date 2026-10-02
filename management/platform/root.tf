# =============================================================================
# A site's platform root: what is put on a cluster that already stands.
#
# Everything it creates is the module's (modules/infrastructure/platform).
# What is here is what makes this a root: the kubernetes provider, configured
# from the cluster root's outputs handed in as variables (versions.tf), and
# where its state is kept (backend_pg.tf.disabled).
# =============================================================================

variable "release" {
  type        = string
  default     = ""
  description = <<-EOT
    Where the platform's releases are published: the registry address of this
    repository's platform package. Handed in by the contractor, which reads
    the repository's name rather than having it written here.
  EOT
}

variable "digest" {
  type        = string
  default     = ""
  description = <<-EOT
    Which release of the platform this root runs: the digest of the package,
    from the site's line in management/versions.json. A digest and not the
    version beside it, because a version is a name a registry can be made to
    answer differently and a digest is the contents.
  EOT
}

variable "unreleased" {
  type        = string
  default     = ""
  description = <<-EOT
    A tree holding the modules as they are in a checkout, read instead of a
    release. For a check that has to see a change before it is released, and
    never for a run against a site: the contractor clears it. With none of
    the three given there is no source to read, and the root runs nothing.
  EOT
}

variable "site" {
  type        = string
  default     = "site0"
  description = "Which key in the config's sites map this serves. Checked by the module."
}

variable "config_path" {
  type        = string
  default     = "../../config/management.rendered.json"
  description = "Path to the rendered config JSON, read by the module."
}

variable "cluster_access" {
  type = object({
    host               = string
    ca_certificate     = string
    client_certificate = string
    client_key         = string
  })
  sensitive   = true
  description = <<-EOT
    How to reach the cluster's API: the cluster root's output of the same
    name, handed over by the contractor. Structured rather than a kubeconfig
    to parse, so each certificate is a value of its own - which is what lets
    a plan against the as-built record run with stand-ins.
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

module "platform" {
  source = var.unreleased != "" ? "${var.unreleased}/modules/infrastructure/platform" : "oci://${var.release}//modules/infrastructure/platform?digest=${var.digest}"

  site        = var.site
  config_path = var.config_path
  kubeconfig  = var.kubeconfig

  # Whether this site has been given work: a directory of its own in the
  # Flux tree. Read here, from the repository as it is, and not by the
  # module, which is read as it was released - so giving a site work is the
  # commit that adds the directory, and needs no release.
  site_directory = fileexists("${path.module}/../../clusters/${var.site}/kustomization.yaml")
}

output "state_db_endpoint" {
  value = module.platform.state_db_endpoint
}

output "state_conn_str" {
  value     = module.platform.state_conn_str
  sensitive = true
}

output "gitops_path" {
  value = module.platform.gitops_path
}
