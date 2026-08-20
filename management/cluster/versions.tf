terraform {
  required_version = ">= 1.6.0"

  required_providers {
    proxmox = { source = "bpg/proxmox", version = "~> 0.60.0" }
    talos   = { source = "siderolabs/talos", version = "~> 0.5.0" }
    flux    = { source = "fluxcd/flux", version = "~> 1.3.0" }
  }
}

provider "proxmox" {
  endpoint  = "https://${local.config.hypervisor.url}:8006/"
  api_token = "${local.config.hypervisor.token_id}=${local.config.hypervisor.token_secret}"
  insecure  = true

  ssh { agent = false }
}

provider "flux" {
  kubernetes = {
    host                   = local.kubeconfig.clusters[0].cluster.server
    client_certificate     = base64decode(local.kubeconfig.users[0].user.client-certificate-data)
    client_key             = base64decode(local.kubeconfig.users[0].user.client-key-data)
    cluster_ca_certificate = base64decode(local.kubeconfig.clusters[0].cluster.certificate-authority-data)
  }
  git = {
    url = local.config.git.url
    http = {
      username = "git"
      password = local.config.git.token
    }
  }
}
