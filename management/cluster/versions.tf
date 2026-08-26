terraform {
  required_version = ">= 1.6.0"

  required_providers {
    proxmox    = { source = "bpg/proxmox", version = "~> 0.111.0" }
    talos      = { source = "siderolabs/talos", version = "~> 0.11.0" }
    kubernetes = { source = "hashicorp/kubernetes", version = "~> 2.35" }
    tailscale  = { source = "tailscale/tailscale", version = "~> 0.17" }
    cloudflare = { source = "cloudflare/cloudflare", version = "~> 4.40" }
  }
}

# --- hypervisor: Proxmox VE ---------------------------------------------------
provider "proxmox" {
  # Any node in a Proxmox cluster serves the API, so the first one is fine.
  # Credentials are per-site because two sites are two separate clusters.
  endpoint  = "https://${local.hypervisors[0].ip}:8006/"
  api_token = "${local.site.hypervisor.token_id}=${local.site.hypervisor.token_secret}"
  insecure  = true

  # Unlike the ssh block below, this stays sourced from local.config
  # (Render's ordinary op-inject pass) rather than a targeted read in the
  # Compute phase. The Overlay phase also runs tofu, before Hypervisor, and
  # Terraform requires every declared provider to resolve *some* credential
  # at configure time regardless of which resources are targeted - reading
  # this only in Compute would leave Overlay with none at all. The token is
  # meant to already exist before a full run starts; hypervisor-prep.yml can
  # still rotate an orphaned one as a recovery path, but that only matters
  # mid-recovery, not in normal operation - see compute.go for the detail.

  # Only needed for one operation: turning a downloaded disk image into an
  # actual VM disk (compute.tf's file_id-based disk) runs pvesm/qm commands
  # directly on the node, which the Proxmox API does not expose. API-token
  # auth has no password to fall back on, so this must be explicit - a
  # dedicated, narrowly-sudo-scoped system account (see
  # management/hypervisor/hypervisor-prep.yml), not the API token's identity
  # or root.
  #
  # username/private_key are deliberately not set here from local.config.
  # The hypervisor phase is what creates this credential (hypervisor-prep.yml
  # generates the account and writes it to 1Password), and that phase runs
  # after Render - putting these through the same op-inject pass as
  # everything else would mean Render can never succeed on a brand new site,
  # since the credential it needs would not exist yet. The compute phase
  # resolves these two op:// references directly (a single targeted `op
  # read`, not the whole-template inject) and sets them as
  # PROXMOX_VE_SSH_USERNAME / PROXMOX_VE_SSH_PRIVATE_KEY before invoking
  # tofu, which the provider reads natively - by the time compute runs,
  # hypervisor has already created them, so this resolves on the very first
  # run of a new site with no manual step.
  ssh { agent = false }
}

# --- overlay network: Tailscale ----------------------------------------------
provider "tailscale" {
  # An OAuth client is preferred over a raw API key: it can be scoped to just
  # the ACL and auth-key permissions this project needs, and it does not expire
  # every 90 days the way a personal API key does.
  oauth_client_id     = local.overlay_network.client_id
  oauth_client_secret = local.overlay_network.client_secret
  tailnet             = local.overlay_network.domain
}

# --- object storage: Cloudflare R2 -------------------------------------------
provider "cloudflare" {
  # Bucket lifecycle only. This is the one credential that needs admin scope,
  # it never leaves the workstation, and the Sterilize phase wipes it.
  api_token = local.object_storage.admin_token
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
