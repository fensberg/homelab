# =============================================================================
# Overlay network: tailnet policy and the hypervisor's auth key.
# Vendor: Tailscale (tailscale/tailscale provider).
#
# WHY THIS EXISTS
# ---------------
# The Proxmox host advertises the SDN subnet (10.10.10.0/24) over the overlay
# network so the workstation can reach the Talos nodes. Advertised routes do
# nothing until they are APPROVED, and approving them in a web console is
# exactly the kind of ClickOps this project refuses to have.
#
# autoApprovers solves it: any node carrying the router tag may advertise that
# subnet and it is approved automatically, with no human in the loop.
#
# WARNING
# -------
# tailscale_acl REPLACES the entire tailnet policy file. The policy below is
# the permissive default plus the pieces this project needs, so applying it
# cannot lock you out. If you have hand-written rules, port them in here BEFORE
# the first apply.
# =============================================================================

locals {
  overlay_router_tag = "tag:homelab-router"
}

resource "tailscale_acl" "this" {
  # Required on first apply, when the tailnet still has its default policy.
  overwrite_existing_content = true

  acl = jsonencode({
    # Who is allowed to apply which tags. autogroup:admin keeps this simple;
    # narrow it to specific users if the tailnet gains other operators.
    tagOwners = {
      (local.overlay_router_tag) = ["autogroup:admin"]
    }

    # The point of this file: routes advertised by a tagged node are approved
    # automatically. No console, no clicking, no silent blocking of ignition.
    autoApprovers = {
      routes = {
        (local.base_cidr) = [local.overlay_router_tag]
      }
    }

    # The default "everything reaches everything" rule. Replace with something
    # tighter once the homelab has more than one operator.
    acls = [
      {
        action = "accept"
        src    = ["*"]
        dst    = ["*:*"]
      }
    ]

    ssh = [
      {
        action = "check"
        src    = ["autogroup:member"]
        dst    = ["autogroup:self"]
        users  = ["autogroup:nonroot", "root"]
      }
    ]
  })
}

# Pre-authorized key the Ansible playbook uses to log the hypervisor in.
# It carries the router tag, which is what makes autoApprovers apply to it.
resource "tailscale_tailnet_key" "hypervisor" {
  depends_on = [tailscale_acl.this]

  reusable      = true  # the playbook is re-run; a single-use key would break that
  ephemeral     = false # the hypervisor is not a throwaway node
  preauthorized = true  # no device-approval step, same no-ClickOps reasoning
  description   = "homelab hypervisor subnet router (managed by OpenTofu)"
  tags          = [local.overlay_router_tag]

  # 90 days. Re-running the button before expiry rotates it transparently.
  expiry = 7776000
}

output "overlay_network_auth_key" {
  value     = tailscale_tailnet_key.hypervisor.key
  sensitive = true
}
