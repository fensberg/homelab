terraform {
  required_version = ">= 1.6.0"

  required_providers {
    proxmox    = { source = "bpg/proxmox", version = "~> 0.111.0" }
    talos      = { source = "siderolabs/talos", version = "~> 0.11.0" }
    flux       = { source = "fluxcd/flux", version = "~> 1.9.0" }
    kubernetes = { source = "hashicorp/kubernetes", version = "~> 2.35" }
    tailscale  = { source = "tailscale/tailscale", version = "~> 0.17" }
    cloudflare = { source = "cloudflare/cloudflare", version = "~> 4.40" }
  }
}

# --- hypervisor: Proxmox VE ---------------------------------------------------
provider "proxmox" {
  endpoint  = "https://${local.config.hypervisor.endpoint}:8006/"
  api_token = "${local.config.hypervisor.token_id}=${local.config.hypervisor.token_secret}"
  insecure  = true

  ssh { agent = false }
}

# --- overlay network: Tailscale ----------------------------------------------
provider "tailscale" {
  # An OAuth client is preferred over a raw API key: it can be scoped to just
  # the ACL and auth-key permissions this project needs, and it does not expire
  # every 90 days the way a personal API key does.
  oauth_client_id     = local.config.overlay_network.client_id
  oauth_client_secret = local.config.overlay_network.client_secret
  tailnet             = local.config.overlay_network.domain
}

# --- object storage: Cloudflare R2 -------------------------------------------
provider "cloudflare" {
  api_token = local.config.object_storage.api_token
}

# --- cluster access -----------------------------------------------------------
# Both of these read the kubeconfig the Talos resources produce. Use the
# provider's structured output rather than parsing raw YAML: yamldecode() of an
# attribute that does not exist yet is unknown at plan time, and indexing into
# an unknown value is fragile.
provider "kubernetes" {
  host                   = talos_cluster_kubeconfig.this.kubernetes_client_configuration.host
  client_certificate     = base64decode(talos_cluster_kubeconfig.this.kubernetes_client_configuration.client_certificate)
  client_key             = base64decode(talos_cluster_kubeconfig.this.kubernetes_client_configuration.client_key)
  cluster_ca_certificate = base64decode(talos_cluster_kubeconfig.this.kubernetes_client_configuration.ca_certificate)
}

# --- gitops: Flux -------------------------------------------------------------
provider "flux" {
  kubernetes = {
    host                   = talos_cluster_kubeconfig.this.kubernetes_client_configuration.host
    client_certificate     = base64decode(talos_cluster_kubeconfig.this.kubernetes_client_configuration.client_certificate)
    client_key             = base64decode(talos_cluster_kubeconfig.this.kubernetes_client_configuration.client_key)
    cluster_ca_certificate = base64decode(talos_cluster_kubeconfig.this.kubernetes_client_configuration.ca_certificate)
  }
  git = {
    url = local.config.source_control.repo_url
    http = {
      username = "git"
      password = local.config.source_control.token
    }
  }
}
