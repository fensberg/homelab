# The providers this module's resources come from. Which versions, and how
# each is configured, is the root's: management/cluster/ holds the constraints,
# the lock file and the provider blocks, so a version is pinned in one place
# and a credential is read in one place.
terraform {
  required_version = ">= 1.9.0"

  required_providers {
    proxmox   = { source = "bpg/proxmox" }
    talos     = { source = "siderolabs/talos" }
    tailscale = { source = "tailscale/tailscale" }
  }
}
