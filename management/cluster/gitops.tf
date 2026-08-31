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
# git instead: OpenTofu creates the namespace, then a one-time kubectl apply
# installs Flux's controllers and points them at this repository. From then
# on Flux reconciles the rest - the CloudNativePG operator and the state
# database - entirely on its own, the same as it always would have.
#
# The sync is anonymous, and there is no credential here to create. This
# repository is public, so the GitRepository's https:// clone succeeds
# unauthenticated; the secret that used to live here held a source-control
# token to authenticate a request that never needed authenticating. The cost
# was not theoretical - a secret OpenTofu creates is a secret in OpenTofu
# state, so it made a leaked state file yield a live token in exchange for
# nothing. If this repository is ever made private, the credential comes back
# with that cost attached and belongs in the decision, not in a quiet edit;
# tests/go/repo/gitops_test.go says so where somebody would be about to.
# =============================================================================

resource "kubernetes_namespace" "flux_system" {
  depends_on = [data.talos_cluster_health.this]

  metadata {
    name = "flux-system"
  }

  # Flux labels its own namespace once it is running - app.kubernetes.io/*
  # and kustomize.toolkit.fluxcd.io/*. OpenTofu did not put them there and
  # would strip them on every run, so Flux re-added them, so the next converge
  # showed the same update again.
  #
  # Harmless on its own, and that is the problem: a change that appears in
  # every plan is a change nobody reads, and it is the one a real change hides
  # behind. Ownership of those labels belongs to Flux.
  lifecycle {
    ignore_changes = [metadata[0].labels]
  }
}

resource "terraform_data" "flux_bootstrap_apply" {
  depends_on = [kubernetes_namespace.flux_system]

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
      export KUBECONFIG="$tmp"
      flux_system="${path.module}/../../${local.gitops_target_path}/flux-system"

      # Two applies, not one kubectl apply -k: a single invocation builds its
      # REST-mapping cache once at the start, before the CRDs it is about to
      # create exist, so gotk-sync.yaml's GitRepository/Kustomization objects
      # fail with "no matches for kind" even though the CRDs were created
      # moments earlier in the same command. This is the documented
      # kubectl/CRD ordering limitation, and exactly why `flux bootstrap`
      # applies components and sync manifests as two separate steps
      # internally - this replicates that, waiting on the
      # app.kubernetes.io/part-of=flux label rather than a hardcoded CRD
      # list so a future Flux version adding CRDs doesn't go stale here.
      kubectl apply -f "$flux_system/gotk-components.yaml"
      kubectl wait --for condition=established --timeout=60s \
        crd -l app.kubernetes.io/part-of=flux
      kubectl apply -f "$flux_system/gotk-sync.yaml"
    EOT
  }
}
