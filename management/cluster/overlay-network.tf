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
  # to be replaced on every run (see scripts/contractor/internal/phases/overlay.go).
  # Without that, re-running `task configure-hypervisor` an hour later would
  # hand the playbook a key tofu still believes is current.
  overlay_key_expiry_seconds = 3600

  # The node key is used again on every node replacement, so it cannot expire
  # in an hour. 90 days is Tailscale's maximum; a node that has not been
  # replaced in that time will need a converge to mint a new one, which is the
  # same forcing function that keeps the hypervisor key fresh.
  overlay_node_key_expiry_seconds = 7776000

  # A separate tag from the router: nodes are not subnet routers and must not
  # inherit a tag whose ACL grants route approval. Keeping them distinct is
  # what lets the tailnet policy say "nodes may reach the hypervisor's API and
  # nothing else".
  overlay_node_tag = "tag:homelab-node"
}

resource "tailscale_tailnet_key" "hypervisor" {
  # Exists only while something is about to use it. See
  # var.overlay_key_wanted for why this is conditional rather than
  # long-lived: an unconditional key was replaced by every untargeted apply,
  # so a converge minted one nothing would read and dropped it (#138).
  count = var.overlay_key_wanted ? 1 : 0

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

# The cluster's own membership of the overlay.
#
# Separate from the hypervisor's key rather than shared, because the two are
# different kinds of device with different lifetimes and different tags. A
# node is disposable - it is replaced whenever the image changes - so its key
# is ephemeral, and the device disappears from the tailnet when the node does
# rather than accumulating dead entries the way four stale router keys once
# did in the console.
#
# Why the cluster joins the overlay at all: the node subnet lives in an EVPN
# VRF, and a VRF cannot deliver to a local address in another VRF. That makes
# the hypervisor's management address unreachable from a pod no matter what is
# routed, because the destination is the host itself and delivery is decided by
# which VRF the listening socket is bound to. The overlay sidesteps it - a
# node reaches the hypervisor as a tailnet peer, and access is governed by
# tailnet ACLs rather than by a subnet.
resource "tailscale_tailnet_key" "nodes" {
  reusable      = true # every node in the cluster registers with this one key
  ephemeral     = true # a replaced node should not leave a device behind
  preauthorized = true

  description = "${local.site_name} cluster nodes"
  tags        = [local.overlay_node_tag]

  # Longer than the hypervisor's hour: this key is baked into machine
  # configuration and used again whenever a node is replaced or rejoins, not
  # consumed once at the end of a playbook run.
  expiry = local.overlay_node_key_expiry_seconds
}

# Null whenever the key does not exist, which is every run except the Overlay
# phase. `one()` over the count list rather than [0], so an apply that has
# revoked the key produces an empty output instead of an index error.
output "overlay_network_auth_key" {
  value     = one(tailscale_tailnet_key.hypervisor[*].key)
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
  value = try(one(one(tailscale_tailnet_key.hypervisor[*].tags)), null)
}
