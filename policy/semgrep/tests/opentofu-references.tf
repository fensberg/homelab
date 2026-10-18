resource "terraform_data" "waits" {
  # ok: nothing-reads-a-value-from-the-health-gate
  depends_on = [data.talos_cluster_health.this]
  input = {
    # ruleid: nothing-reads-a-value-from-the-health-gate
    nodes = data.talos_cluster_health.this[0].control_plane_nodes
    # ruleid: nothing-reads-a-value-from-the-health-gate
    other = data.talos_cluster_health.this.id
    # ok: nothing-reads-a-value-from-the-health-gate
    note = "data.talos_cluster_health.this.id is described here, not read"
  }
}

locals {
  # ruleid: no-git-credential-is-created-for-flux
  token = local.config.source_control.token
  # ruleid: no-git-credential-is-created-for-flux
  path = "op://estate/source_control/token"
  # ok: no-git-credential-is-created-for-flux
  repo = local.config.source_control.repo_url
}

# ruleid: no-git-credential-is-created-for-flux
resource "kubernetes_secret" "flux_system_git_auth" {
  metadata {
    name = "flux-system"
  }
}

resource "terraform_data" "reads" {
  # ruleid: no-git-credential-is-created-for-flux
  input = kubernetes_secret.flux_system_git_auth.id
}
