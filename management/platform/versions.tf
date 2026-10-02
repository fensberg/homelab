terraform {
  required_version = ">= 1.10.0"

  required_providers {
    kubernetes = { source = "hashicorp/kubernetes", version = "~> 2.35" }
  }
}

# Configured from an input, never from a resource: the cluster root has
# already applied by the time anything here plans, so this is known at plan
# time and an import or a targeted plan resolves it like any other value.
provider "kubernetes" {
  host                   = var.cluster_access.host
  client_certificate     = base64decode(var.cluster_access.client_certificate)
  client_key             = base64decode(var.cluster_access.client_key)
  cluster_ca_certificate = base64decode(var.cluster_access.ca_certificate)
}
