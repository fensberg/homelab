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

  # The code in this root speaks to exactly one vendor per concern. These now
  # sit inside the site, because which vendor a site uses is a property of that
  # estate: site 0 might join your tailnet while site 1 joins a client's.
  #
  # source_control is deliberately absent: flux_bootstrap_git speaks plain git
  # over HTTPS and works against GitHub, GitLab or Gitea alike, and it is
  # fleet-wide anyway - one repository drives every cluster.
  required_providers_by_concern = {
    hypervisor      = "proxmox"
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
      condition = alltrue([
        for concern, want in local.required_providers_by_concern :
        try(local.site[concern].provider, null) == want
      ])
      error_message = "Provider mismatch in sites[${var.site_index}]. This root implements ${join(", ", [for c, p in local.required_providers_by_concern : "${c}=${p}"])}. Change the code before changing the declaration - the check exists to stop one vendor's credentials reaching another vendor's API."
    }
  }
}
