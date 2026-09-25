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
# Three owners. The estate creates the tunnel and its routes and decides who
# may enroll (management/estate/); this root creates the namespace and the
# secret the connector runs from, out of the run token the estate granted; the
# connector itself is declared in git under clusters/management/ and
# reconciled by Flux.
#
# GENERAL, NOT PER-WORKLOAD. This is how anything in the estate is reached
# from away - a phone, a laptop in another house - so nothing here is named
# after the workload that happened to need it first. That was the game server
# (#446), whose crossplay backend cannot hold a player on a strict network and
# whose alternative - a port forward - would have moved it into a dedicated
# zone. Grafana is next, and a route is one line each time.
# =============================================================================

# The tunnel itself, its routes and its configuration are the estate's
# (management/estate/site/tunnel.tf): creating one needs a token that can
# create and delete every tunnel in the account. This site is granted the run
# token alone, which serves this one tunnel and can change nothing.

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
    token = local.tunnel.token
  }
}
