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

  # One hour, not the 90-day maximum this used to carry.
  #
  # The key is only ever used at one moment: the hypervisor's `tailscale up`,
  # minutes after this resource is created. It has no job after that, because
  # a *tagged* device does not expire - key expiry is disabled for tagged
  # nodes, so the node stays on the tailnet indefinitely and a cluster 91 days
  # from now still routes. What a long expiry actually buys is a valid,
  # pre-authorized, route-approving credential sitting in a console for three
  # months after the only thing that needed it has finished. Four of them had
  # accumulated by the end of epoch 01.
  #
  # A short expiry is only safe because the Overlay phase forces this resource
  # to be replaced on every run (see scripts/steward/internal/phases/overlay.go).
  # Without that, re-running `task configure-hypervisor` an hour later would
  # hand the playbook a key tofu still believes is current.
  overlay_key_expiry_seconds = 3600
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

  # See the local for why this is an hour rather than the 90-day maximum.
  expiry = local.overlay_key_expiry_seconds
}

output "overlay_network_auth_key" {
  value     = tailscale_tailnet_key.hypervisor.key
  sensitive = true
}

# The playbook has to know which tag to check the host against, and it must be
# the same tag the key carries.
#
# Read off the created key rather than from local.overlay_router_tag, for two
# reasons. It is the tag that actually exists rather than one that merely ought
# to match. And the Overlay phase applies with -target, which only refreshes
# outputs depending on the targeted resource - an output built from a bare
# local depends on nothing and is never written to state, so `tofu output`
# cannot find it.
output "overlay_router_tag" {
  value = one(tailscale_tailnet_key.hypervisor.tags)
}
