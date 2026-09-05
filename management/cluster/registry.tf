# =============================================================================
# Invariants on the config contract.
#
# Preconditions rather than tests, deliberately: a test has to be remembered
# and run, while a precondition fails `tofu plan` and cannot be skipped.
# =============================================================================

locals {
  all_octets = [for k, s in local.config.sites : s.octet]

  # Octets must stay clear of the Kubernetes defaults at 10.96.0.0/12
  # (services) and 10.244.0.0/16 (pods). Those are cluster-internal and never
  # routed over the overlay, but overlapping them makes debugging confusing.
  octet_min = 1
  octet_max = 95

  # What this root actually implements, per concern. source_control is absent
  # on purpose: Flux's GitRepository source speaks plain git over HTTPS and
  # works against GitHub, GitLab or Gitea alike, so asserting a vendor the
  # code does not depend on would be noise rather than a guard.
  required_providers_by_concern = {
    hypervisor      = "proxmox"
    overlay_network = "tailscale"
    object_storage  = "cloudflare"
  }

  # Access key IDs that begin AKIA or ASIA are AWS long-term and temporary
  # credentials respectively. R2 issues 32 hex characters, so this prefix is a
  # positive identification rather than a heuristic.
  object_storage_key = try(local.object_storage.access_key_id, "")
  looks_like_aws_key = can(regex("^(AKIA|ASIA)", local.object_storage_key))
}

resource "terraform_data" "invariants" {
  lifecycle {
    # "Is var.site a real site?" is deliberately NOT here. It used to be, and
    # it was dead code: local.site indexes sites[var.site] directly, so an
    # unknown site failed on a raw "Invalid index" against variables.tf before
    # any precondition here was ever evaluated. It now lives as a validation
    # block on the variable itself, which runs before locals are evaluated and
    # cannot be targeted away. See variables.tf.

    precondition {
      condition     = length(local.all_octets) == length(distinct(local.all_octets))
      error_message = "Duplicate octet in sites. Each site owns 10.<octet>.0.0/16; two sites sharing one collide on the overlay network and present as a broken network rather than a config mistake."
    }

    precondition {
      condition     = alltrue([for o in local.all_octets : o >= local.octet_min && o <= local.octet_max])
      error_message = "Octet out of range. Use ${local.octet_min}-${local.octet_max}: Kubernetes defaults occupy 10.96.0.0/12 and 10.244.0.0/16."
    }

    precondition {
      condition     = length(local.hypervisors) > 0
      error_message = "Site '${var.site}' has no hypervisor nodes. Add at least one to sites.${var.site}.hypervisor.nodes."
    }

    # A node's identity must be its host octet, not its position in a list.
    #
    # With count the key is a position, so removing a node renumbers every node
    # after it and OpenTofu resolves that as destroying and recreating each one
    # - a running etcd member torn down for being third instead of fourth, with
    # nothing calling `talosctl etcd remove-member` first. Keyed by host octet,
    # replacing one node is one create and one destroy.
    #
    # Checked here rather than in a test because a test can only assert it for
    # the fixtures somebody wrote; this asserts it for whatever config is
    # actually being deployed, at plan time, before anything is built.
    precondition {
      condition     = alltrue([for k, v in local.control_plane : k == tostring(v.host_octet)])
      error_message = "control_plane is not keyed by host octet, so a node's identity is its position rather than the machine. Removing one node would renumber and replace the others."
    }

    # One number per node, used three ways. They were three different numbers
    # once - .100 was cp-01 was vm 1000 - and every cross-reference between
    # Proxmox, kubectl and the network needed arithmetic.
    precondition {
      condition = alltrue([
        for k, v in local.control_plane :
        endswith(v.name, "-cp-${k}") && endswith(tostring(v.vm_id), k) && endswith(v.ip, ".${k}")
      ])
      error_message = "A node's name, VM id and address must all end in its host octet. They are meant to be one number read three ways."
    }

    precondition {
      condition     = local.node_count >= 1
      error_message = "control_plane_count must be at least 1. Use an odd number: etcd needs a quorum, and an even count adds a member without adding a tiebreaker."
    }

    # Workers get the same three-ways-one-number check the control plane has,
    # because the property is the point rather than the resource: a machine's
    # name, id and address all end in its host octet so an incident needs no
    # arithmetic to cross-reference them.
    precondition {
      condition = alltrue([
        for k, v in local.workers :
        endswith(v.name, "-wk-${k}") && endswith(tostring(v.vm_id), k) && endswith(v.ip, ".${k}")
      ])
      error_message = "A worker's name, VM id and address must all end in its host octet. They are meant to be one number read three ways."
    }

    # The bands must not meet.
    #
    # talos.tf merges the control plane and the workers into one map to share
    # their config patches, and a duplicate key there would silently drop a
    # machine rather than fail - the merge keeps the last value. This is what
    # makes that merge safe, so it is a precondition rather than a comment.
    # 100 control planes is already far past what etcd should ever run.
    precondition {
      condition     = length(setintersection(toset(keys(local.control_plane)), toset(keys(local.workers)))) == 0
      error_message = "A control plane and a worker share a host octet. The bands are 100+ for control planes and 200+ for workers; they must not overlap."
    }

    precondition {
      condition     = local.worker_count >= 0 && local.worker_count <= 55
      error_message = "worker_count must be between 0 and 55. Zero is valid and means no workers; the ceiling keeps the 200+ band inside a single octet."
    }

    # --- vendor lock, checked three ways -----------------------------------
    #
    # 1. The code implements one vendor per concern.
    # 2. The config declares one, in git, where it is reviewable.
    # 3. The vault attests one, travelling with the credentials themselves.
    #
    # Checking only 1 against 2 compares two things that are both in git and
    # both change together in the same commit. It cannot see the failure that
    # actually matters: someone swapping the vault item's contents for another
    # vendor's credentials while every file here stays untouched.
    precondition {
      condition = alltrue([
        for concern, want in local.required_providers_by_concern :
        try(local.site[concern].provider, null) == want
      ])
      error_message = "Provider mismatch in site '${var.site}': the config declares a vendor this root does not implement. It implements ${join(", ", [for c, p in local.required_providers_by_concern : "${c}=${p}"])}. Change the code before changing the declaration."
    }

    precondition {
      condition = alltrue([
        for concern, want in local.required_providers_by_concern :
        try(local.site[concern].vault_provider, null) == want
      ])
      error_message = "Vault attestation mismatch in site '${var.site}': a 1Password item declares a different vendor than the config does. Either the wrong item is referenced, or its credentials were replaced without updating its provider field. This is the check that stops one vendor's credentials reaching another vendor's API."
    }

    # A declaration only catches someone who updates the declaration. This
    # catches the careless case: credentials pasted in without touching the
    # provider field at all.
    precondition {
      condition     = !local.looks_like_aws_key
      error_message = "sites.${var.site}.object_storage.access_key_id begins with AKIA or ASIA, which is an AWS credential, but this site declares cloudflare. R2 access key IDs are 32 hex characters."
    }
  }
}
