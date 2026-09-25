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
  # What an enrolled device may reach, by name. Each is a fixed cluster
  # address, set as `clusterIP` on the Service it names - the two are held
  # together by tests/go/repo/tunnel_routes_test.go, because a route to an
  # address nothing answers on converges cleanly and reaches nothing.
  #
  # Addresses come from the bottom of the service range, which Kubernetes
  # reserves for addresses chosen by hand, so none can already be taken by a
  # Service that was allocated one.
  tunnel_routes = {
    "game-server" = "10.96.0.46"
  }

  tunnel_members = [for m in split(",", local.tunnel.members) : trimspace(m) if trimspace(m) != ""]
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

# WARP sends ONLY these addresses through Cloudflare. Everything else a device
# does goes out its own connection as it would without WARP.
#
# Include mode rather than the default exclude mode, for two reasons. The
# default excludes every private range - 10.0.0.0/8 among them - so the routes
# above would never enter the tunnel without editing that list. And an include
# list says exactly what an enrolled device is given, where an exclude list
# says everything except.
resource "cloudflare_zero_trust_split_tunnel" "estate" {
  provider   = cloudflare.tunnel
  account_id = local.object_storage_account.account_id
  mode       = "include"

  dynamic "tunnels" {
    for_each = local.tunnel_routes
    content {
      address     = "${tunnels.value}/32"
      description = tunnels.key
    }
  }
}

# Who may enroll a device. The list is personal data, so it lives in the vault.
resource "cloudflare_zero_trust_access_policy" "members" {
  provider   = cloudflare.tunnel
  account_id = local.object_storage_account.account_id
  name       = "estate members"
  decision   = "allow"

  include {
    email = local.tunnel_members
  }

  lifecycle {
    create_before_destroy = true
  }
}

# Device enrollment is an Access application of type `warp`: enrolling a WARP
# client is a login to it, and the policy above decides who succeeds.
#
# THE ORGANISATION'S, NOT THE ESTATE'S. Cloudflare creates this application
# with every Zero Trust organisation and allows only one, so creating it while
# one exists fails with `application_already_exists` (#453). The contractor's
# Cluster phase adopts the one that exists before this is applied, and creates
# it only when there is none; a teardown forgets it rather than destroying it
# (adoptEnrollmentApp and forgetEnrollmentApp, and config.EnrollmentAppAddress).
#
# It was a data source and an import block here, and a demolish destroyed the
# application - after which the data source failed every OpenTofu command that
# evaluated it, which is why adoption lives in Go (see run.AdoptIfOrphaned).
locals {
  enrollment_app_name = "Warp Login App"
}

resource "cloudflare_zero_trust_access_application" "enrollment" {
  provider         = cloudflare.tunnel
  account_id       = local.object_storage_account.account_id
  name             = local.enrollment_app_name
  type             = "warp"
  session_duration = "24h"

  # Declared because the plan otherwise wants to change it on every run: the
  # application is adopted rather than created, so its attributes come from
  # what Cloudflare already had, and an attribute this file does not mention
  # is one the provider defaults differently (#474). A plan that always shows
  # a change teaches whoever reads it to skim, and the next real drift arrives
  # in the same colour as the noise.
  #
  # false because enrolling a device is not something anybody launches from
  # the App Launcher; it happens in the WARP client.
  app_launcher_visible = false
  policies             = [cloudflare_zero_trust_access_policy.members.id]
}

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
