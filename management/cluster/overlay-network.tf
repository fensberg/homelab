# =============================================================================
# Overlay network: the auth key this site uses to join the tailnet.
# Vendor: Tailscale (tailscale/tailscale provider).
#
# WHAT IS *NOT* HERE
# ------------------
# The tailnet policy - tagOwners and autoApprovers - is deliberately not
# managed from this root. It is a property of the tailnet, not of any one
# deployment, and tailscale_acl replaces the policy file wholesale. Managing it
# here would mean every site deployment clobbers the policy every other site
# depends on.
#
# Set the policy up once per tailnet instead: see docs/tailnet-setup.md.
#
# WHAT THIS DOES
# --------------
# Mints a fresh, pre-authorized, tagged auth key for the hypervisor. Because
# the tailnet policy already auto-approves this subnet for the router tag, the
# advertised route is approved with no console interaction.
#
# An OAuth client is used rather than a stored auth key because auth keys
# expire (90 days maximum) and a pre-baked one becomes an expired-credential
# failure at exactly the wrong moment. An OAuth client does not expire and
# mints a fresh key on every run.
# =============================================================================

locals {
  overlay_router_tag = "tag:homelab-router"
}

resource "tailscale_tailnet_key" "hypervisor" {
  reusable      = true  # the playbook is re-run; a single-use key would break that
  ephemeral     = false # the hypervisor is not a throwaway node
  preauthorized = true  # no device-approval step, same no-ClickOps reasoning
  # Tailscale caps this at 50 characters and rejects punctuation - parentheses
  # come back as "description had invalid characters (400)". Letters, digits
  # and spaces only.
  #
  # Naming it for the site means the Tailscale console shows which estate a
  # key belongs to, which matters once more than one site shares a tailnet.
  description = "${local.site_name} subnet router"
  tags        = [local.overlay_router_tag]

  # 90 days. A fresh key is minted on every run, so this is only an upper bound
  # on how long an unused one stays valid.
  expiry = 7776000
}

output "overlay_network_auth_key" {
  value     = tailscale_tailnet_key.hypervisor.key
  sensitive = true
}

# The playbook has to know which tag to check the host against, and it must be
# the same tag the key carries. Exported rather than repeated so the two cannot
# drift apart.
output "overlay_router_tag" {
  value = local.overlay_router_tag
}
