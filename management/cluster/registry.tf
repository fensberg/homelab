# =============================================================================
# Invariants on the site registry and the config contract.
#
# These are preconditions rather than tests, deliberately. A test has to be
# remembered and run; a precondition fails `tofu plan` and cannot be skipped,
# which is the guarantee actually wanted here: it is not possible to apply a
# configuration with two sites on the same octet.
# =============================================================================

locals {
  registry = jsondecode(file("../../config/sites.json")).sites
  octets   = [for k, v in local.registry : v.octet]

  # Kubernetes defaults occupy 10.96.0.0/12 (services) and 10.244.0.0/16
  # (pods). Those are cluster-internal and never routed over the overlay
  # network, but overlapping them makes debugging needlessly confusing.
  octet_min = 1
  octet_max = 95

  # The code in this root speaks to exactly one vendor per concern. Each entry
  # is what the resources here actually require, so a swapped vault item fails
  # with a readable message instead of throwing one vendor's credentials at
  # another vendor's API.
  required_providers_by_concern = {
    hypervisor      = "proxmox"   # bpg/proxmox, pvesh, Proxmox SDN
    overlay_network = "tailscale" # tailscale_tailnet_key, tailscale CLI
    object_storage  = "cloudflare"
  }

  # source_control is deliberately absent: flux_bootstrap_git speaks plain git
  # over HTTPS and works against GitHub, GitLab or Gitea alike. Asserting a
  # vendor the code does not depend on would be noise, not a guard.
}

resource "terraform_data" "invariants" {
  lifecycle {
    precondition {
      condition     = length(local.octets) == length(distinct(local.octets))
      error_message = "Duplicate octet in config/sites.json. Each site owns 10.<octet>.0.0/16; two sites sharing one collide on the overlay network and present as a broken network rather than a config mistake."
    }

    precondition {
      condition     = alltrue([for o in local.octets : o >= local.octet_min && o <= local.octet_max])
      error_message = "Octet out of range in config/sites.json. Use ${local.octet_min}-${local.octet_max}: Kubernetes defaults occupy 10.96.0.0/12 and 10.244.0.0/16."
    }

    precondition {
      condition     = contains(keys(local.registry), var.site)
      error_message = "Unknown site '${var.site}'. config/sites.json defines: ${join(", ", sort(keys(local.registry)))}."
    }

    precondition {
      condition     = contains(keys(local.fleet), var.site)
      error_message = "Site '${var.site}' is in config/sites.json but not in the fleet document at op://homelab/topology/fleet. The two must agree - see config/fleet.example.json."
    }

    precondition {
      condition     = length(local.hypervisors) > 0
      error_message = "Site '${var.site}' has no hypervisor nodes in the fleet document."
    }

    precondition {
      condition = alltrue([
        for concern, want in local.required_providers_by_concern :
        try(local.config[concern].provider, null) == want
      ])
      error_message = "Provider mismatch in config/management.tpl.json. This root implements ${join(", ", [for c, p in local.required_providers_by_concern : "${c}=${p}"])}. Change the code before changing the declaration - the point of the check is to stop one vendor's credentials reaching another vendor's API."
    }
  }
}
