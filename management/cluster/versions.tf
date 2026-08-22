terraform {
  required_version = ">= 1.6.0"

  required_providers {
    proxmox   = { source = "bpg/proxmox", version = "~> 0.111.0" }
    talos     = { source = "siderolabs/talos", version = "~> 0.11.0" }
    flux      = { source = "fluxcd/flux", version = "~> 1.9.0" }
    tailscale = { source = "tailscale/tailscale", version = "~> 0.17" }
    cloudflare = { source = "cloudflare/cloudflare", version = "~> 4.40" }
  }
}

provider "proxmox" {
  endpoint  = "https://${local.config.hypervisor.url}:8006/"
  api_token = "${local.config.hypervisor.token_id}=${local.config.hypervisor.token_secret}"
  insecure  = true

  ssh { agent = false }
}

provider "tailscale" {
  # An OAuth client is preferred over a raw API key: it can be scoped to just
  # the ACL and auth-key permissions this project needs, and it does not expire
  # every 90 days the way a personal API key does.
  oauth_client_id     = local.config.tailscale.oauth_client_id
  oauth_client_secret = local.config.tailscale.oauth_client_secret
  tailnet             = local.config.tailscale.tailnet
}

provider "cloudflare" {
  api_token = local.config.state.r2.api_token
}

provider "flux" {
  kubernetes = {
    # Use the provider's structured output rather than parsing the raw
    # kubeconfig YAML. yamldecode() of an attribute that does not exist yet is
    # unknown at plan time, and indexing into an unknown value is fragile.
    host                   = talos_cluster_kubeconfig.this.kubernetes_client_configuration.host
    client_certificate     = base64decode(talos_cluster_kubeconfig.this.kubernetes_client_configuration.client_certificate)
    client_key             = base64decode(talos_cluster_kubeconfig.this.kubernetes_client_configuration.client_key)
    cluster_ca_certificate = base64decode(talos_cluster_kubeconfig.this.kubernetes_client_configuration.ca_certificate)
  }
  git = {
    url = local.config.git.url
    http = {
      username = "git"
      password = local.config.git.github_pat_reference
    }
  }
}
