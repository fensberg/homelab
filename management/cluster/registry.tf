# =============================================================================
# Invariants on the config contract.
#
# Preconditions rather than tests, deliberately: a test has to be remembered
# and run, while a precondition fails `tofu plan` and cannot be skipped.
#
# Note what is NOT checked here. Octet uniqueness needs no assertion, because
# the octet derives from the array index and two entries cannot share an index.
# The schema makes the collision inexpressible, which is a stronger guarantee
# than any test.
# =============================================================================

locals {
  # 10 + index must stay clear of the Kubernetes defaults at 10.96.0.0/12
  # (services) and 10.244.0.0/16 (pods). Those are cluster-internal and never
  # routed over the overlay, but overlapping them makes debugging confusing.
  max_site_index = 85

  # The code in this root speaks to exactly one vendor per concern. Each entry
  # is what the resources here actually require, so a swapped vault item fails
  # with a readable message instead of throwing one vendor's credentials at
  # another vendor's API.
  #
  # source_control is deliberately absent: flux_bootstrap_git speaks plain git
  # over HTTPS and works against GitHub, GitLab or Gitea alike. Asserting a
  # vendor the code does not depend on would be noise, not a guard.
  required_providers_by_concern = {
    overlay_network = "tailscale"
    object_storage  = "cloudflare"
  }
}

resource "terraform_data" "invariants" {
  lifecycle {
    precondition {
      condition     = var.site_index >= 0 && var.site_index < length(local.config.sites)
      error_message = "site_index ${var.site_index} is out of range: the config defines ${length(local.config.sites)} site(s), so valid indices are 0-${length(local.config.sites) - 1}."
    }

    precondition {
      condition     = var.site_index <= local.max_site_index
      error_message = "site_index ${var.site_index} is too high. The octet is 10 + index and must stay below 96, where the Kubernetes defaults begin."
    }

    precondition {
      condition     = length(local.hypervisors) > 0
      error_message = "Site ${var.site_index} has no hypervisor nodes. Add at least one to sites[${var.site_index}].hypervisor.nodes."
    }

    precondition {
      condition     = local.node_count >= 1
      error_message = "control_plane_count must be at least 1. Use an odd number: etcd needs a quorum, and an even count adds a member without adding a tiebreaker."
    }

    precondition {
      condition     = local.site.hypervisor.provider == "proxmox"
      error_message = "sites[${var.site_index}].hypervisor.provider is '${local.site.hypervisor.provider}', but this root implements proxmox. Change the code before changing the declaration."
    }

    precondition {
      condition = alltrue([
        for concern, want in local.required_providers_by_concern :
        try(local.config[concern].provider, null) == want
      ])
      error_message = "Provider mismatch. This root implements ${join(", ", [for c, p in local.required_providers_by_concern : "${c}=${p}"])}. The point of the check is to stop one vendor's credentials reaching another vendor's API."
    }
  }
}
