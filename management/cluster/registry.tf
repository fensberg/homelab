# =============================================================================
# Invariants on the config contract.
#
# Preconditions rather than tests, deliberately: a test has to be remembered
# and run, while a precondition fails `tofu plan` and cannot be skipped.
#
# Octets are declared rather than computed, so uniqueness is asserted here.
# That is the trade: an explicit octet is readable, survives reordering and
# lets a retired site leave a gap, at the cost of being possible to get wrong -
# so it is checked, and checked across every site rather than just the selected
# one.
# =============================================================================

locals {
  all_octets = [for s in local.config.sites : s.octet]

  # Octets must stay clear of the Kubernetes defaults at 10.96.0.0/12
  # (services) and 10.244.0.0/16 (pods). Those are cluster-internal and never
  # routed over the overlay, but overlapping them makes debugging confusing.
  octet_min = 1
  octet_max = 95

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
      condition     = length(local.all_octets) == length(distinct(local.all_octets))
      error_message = "Duplicate octet in sites[]. Each site owns 10.<octet>.0.0/16; two sites sharing one collide on the overlay network and present as a broken network rather than a config mistake."
    }

    precondition {
      condition     = alltrue([for o in local.all_octets : o >= local.octet_min && o <= local.octet_max])
      error_message = "Octet out of range in sites[]. Use ${local.octet_min}-${local.octet_max}: Kubernetes defaults occupy 10.96.0.0/12 and 10.244.0.0/16."
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
