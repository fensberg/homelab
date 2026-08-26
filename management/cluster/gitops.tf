# =============================================================================
# GitOps controller. Vendor: Flux.
#
# Flux's own install and sync manifests (clusters/management/flux-system/)
# are committed to the repository like every other manifest here, reviewed
# through a normal pull request - not generated and pushed by this apply.
# flux_bootstrap_git (the fluxcd/flux provider's usual bootstrap resource)
# does exactly that generate-and-push itself, which conflicts with this
# repo's branch protection (a required-PR ruleset with no bypass actors) and,
# more fundamentally, with treating every reconciled manifest as reviewed
# code rather than a runtime side effect. So this applies what is already in
# git instead: OpenTofu creates the namespace and the git-credential secret
# (the "namespace and secrets" it always owns), then a one-time kubectl apply
# installs Flux's controllers and points them at this repository. From then
# on Flux reconciles the rest - the CloudNativePG operator and the state
# database - entirely on its own, the same as it always would have.
# =============================================================================

resource "kubernetes_namespace" "flux_system" {
  depends_on = [talos_cluster_kubeconfig.this]

  metadata {
    name = "flux-system"
  }
}

resource "kubernetes_secret" "flux_system_git_auth" {
  depends_on = [kubernetes_namespace.flux_system]

  metadata {
    name      = "flux-system"
    namespace = "flux-system"
  }

  data = {
    username = "git"
    password = local.config.source_control.token
  }
}

resource "terraform_data" "flux_bootstrap_apply" {
  depends_on = [kubernetes_secret.flux_system_git_auth]

  # Re-applies whenever the committed manifests change, not just on first
  # create - a Flux version bump or a new controller lands the same way any
  # other reviewed change to this path does.
  triggers_replace = [
    filesha256("${path.module}/../../${local.gitops_target_path}/flux-system/gotk-components.yaml"),
    filesha256("${path.module}/../../${local.gitops_target_path}/flux-system/gotk-sync.yaml"),
  ]

  provisioner "local-exec" {
    # The kubeconfig is written to a file that exists only for the lifetime
    # of this one command - kubectl (unlike the kubernetes/talos providers
    # used everywhere else here) has no way to authenticate from in-memory
    # values, and this project's own invariant is that nothing survives on
    # the workstation past the run that needed it.
    environment = {
      KUBECONFIG_CONTENT = talos_cluster_kubeconfig.this.kubeconfig_raw
    }
    command = <<-EOT
      set -euo pipefail
      tmp=$(mktemp)
      trap 'rm -f "$tmp"' EXIT
      printf '%s' "$KUBECONFIG_CONTENT" >"$tmp"
      KUBECONFIG="$tmp" kubectl apply -k "${path.module}/../../${local.gitops_target_path}/flux-system"
    EOT
  }
}
