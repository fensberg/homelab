# =============================================================================
# The tunnel. Vendor: Cloudflare Tunnel, with WARP for enrolled devices.
#
# How somebody off the LAN reaches a service inside the estate without a port
# forward on the router. cloudflared runs in the cluster and dials OUT to
# Cloudflare; a device running Cloudflare's WARP client, enrolled by somebody
# on the member list, is handed a route to specific addresses inside the
# cluster and reaches them through that connection. Nothing listens on the
# house's public address.
#
# NOT the overlay. The operator's laptop joins Cloudflare, never the tailnet:
# "Nothing touches it besides actual workers doing work." What an enrolled
# device can reach is exactly the routes below, not the house and not the
# cluster.
#
# The same division as monitoring.tf: OpenTofu creates the tunnel, its routes,
# who may enroll, and the namespace and secret the connector runs from; the
# connector itself is declared in git under clusters/management/ and reconciled
# by Flux.
#
# GENERAL, NOT PER-WORKLOAD. This is how anything in the estate is reached
# from away - a phone, a laptop in another house - so nothing here is named
# after the workload that happened to need it first. That was the game server
# (#446), whose crossplay backend cannot hold a player on a strict network and
# whose alternative - a port forward - would have moved it into a dedicated
# zone. Grafana is next, and a route is one line each time.
# =============================================================================

locals {
  # What an enrolled device may reach, by name. Written once, in
  # management/tunnel-routes.json, because the estate reads the same list for
  # the account-wide split tunnel.
  tunnel_routes = jsondecode(file("${path.module}/../tunnel-routes.json")).routes
}

# WHERE THE ACCOUNT ID COMES FROM. `object_storage.account_id`, which reads
# oddly and is correct today: it is the Cloudflare ACCOUNT the estate has, and
# object storage was simply the first thing in it. It arrives from the vault at
# op://homelab/object_storage/account_id, from the account's dashboard. That
# the tunnel has to reach into object storage to find it is a defect in where
# the key lives rather than here; #457 moves it.
resource "cloudflare_zero_trust_tunnel_cloudflared" "estate" {
  provider   = cloudflare.tunnel
  account_id = local.object_storage_account.account_id
  name       = "${local.config.organization.name}-${var.site}"

  # The tunnel's password, which Cloudflare turns into the token cloudflared
  # runs from. Nobody types it and nobody reads it: the contractor generates 44
  # random characters into op://homelab/tunnel/secret on the run before this
  # one (phases/secrets.go, `ensureTunnelSecret`), and Render brings it back as
  # local.tunnel.secret. Generated rather than human-supplied because it is
  # written to state as a resource attribute, which is the rule that decides
  # which secrets this project owns end to end.
  secret     = base64encode(local.tunnel.secret)
  config_src = "cloudflare"
}

# Private routing only. There is no public hostname on this tunnel: every
# destination is reached by an enrolled device, never by an address on the
# internet. The catch-all answers anything else with a 404.
resource "cloudflare_zero_trust_tunnel_cloudflared_config" "estate" {
  provider   = cloudflare.tunnel
  account_id = local.object_storage_account.account_id
  tunnel_id  = cloudflare_zero_trust_tunnel_cloudflared.estate.id

  config {
    warp_routing {
      enabled = true
    }
    ingress_rule {
      service = "http_status:404"
    }
  }
}

resource "cloudflare_zero_trust_tunnel_route" "estate" {
  for_each = local.tunnel_routes

  provider   = cloudflare.tunnel
  account_id = local.object_storage_account.account_id
  tunnel_id  = cloudflare_zero_trust_tunnel_cloudflared.estate.id
  network    = "${each.value}/32"
  comment    = each.key
}

# The account-wide half - who may enroll a device (the Access policy), the
# enrollment application, and the split tunnel that decides what an enrolled
# device sends through Cloudflare - is the estate's, and lives in
# management/estate/. A site's build cannot reach it, so a site demolish cannot
# delete it (#531).

# The connector's namespace and credential, for the Deployment Flux applies.
resource "kubernetes_namespace" "tunnel" {
  depends_on = [data.talos_cluster_health.this]
  metadata {
    name = "tunnel"
  }
}

# The token cloudflared runs from. It lets whoever holds it serve this one
# tunnel and nothing else in the account.
resource "kubernetes_secret" "tunnel_token" {
  metadata {
    name      = "tunnel-token"
    namespace = kubernetes_namespace.tunnel.metadata[0].name
  }
  data = {
    token = cloudflare_zero_trust_tunnel_cloudflared.estate.tunnel_token
  }
}
