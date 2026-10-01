# The provider this module's resources come from. Its version and its
# configuration are the root's (management/platform/).
terraform {
  required_version = ">= 1.9.0"

  required_providers {
    kubernetes = { source = "hashicorp/kubernetes" }
  }
}
