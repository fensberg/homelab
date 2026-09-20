# =============================================================================
# Monitoring plumbing. Vendor: Prometheus and Grafana, deployed by Flux.
#
# The same division as database.tf: OpenTofu creates only what Flux cannot -
# the namespace and the secret - and the stack itself is declared in git under
# clusters/management/ and reconciled by Flux.
#
# WHY THE NAMESPACE IS HERE RATHER THAN IN THE FLUX MANIFEST. A secret has to
# exist in a namespace, and OpenTofu creating one that Flux also declares is a
# race with two owners: Flux would prune or adopt it depending on which
# reconciled last. So the namespace belongs to whichever side creates the
# secret, which is this one - the same arrangement the state database has.
# =============================================================================
resource "kubernetes_namespace" "monitoring" {
  depends_on = [data.talos_cluster_health.this]
  metadata {
    name = "monitoring"

    # Talos enforces the `baseline` Pod Security standard on every namespace
    # but kube-system, and node-exporter cannot run under it: reading a node's
    # metrics means its PID namespace, hostPath mounts of /proc, /sys and /,
    # and a host port. Baseline refuses all four, so the chart's DaemonSet was
    # refused admission, the HelmRelease failed, and everything behind
    # infra-controllers stopped reconciling - which is what a converge sits in
    # Health waiting for (#459).
    #
    # THE COST, STATED. This raises the bar for the WHOLE namespace, not for
    # node-exporter alone: Prometheus, Alertmanager and Grafana could also
    # mount a host path here, and nothing in Kubernetes scopes the label
    # tighter than a namespace. What bounds it instead is that everything in
    # here arrives by digest from an approved supplier through a reviewed
    # HelmRelease. #460 moves node-exporter into a namespace of its own so the
    # rest can go back to baseline.
    labels = {
      "pod-security.kubernetes.io/enforce" = "privileged"
      # Still reported for anything that would fail the stricter standards, so
      # a workload quietly acquiring host access is visible rather than silent.
      "pod-security.kubernetes.io/audit" = "restricted"
      "pod-security.kubernetes.io/warn"  = "restricted"
    }
  }
}

# The destination Alertmanager posts to.
#
# An incoming webhook URL IS the credential: anything that knows it can post to
# that channel, and there is no second factor. So it arrives from the vault at
# render time and lands here, rather than in the HelmRelease values in git.
#
# Alertmanager reads it from a FILE rather than from its config, which is why
# this is mounted as a secret rather than substituted into the configuration:
# the operator writes the full Alertmanager config into a secret of its own,
# and a URL written there would be readable by anything that can read that
# secret. `slack_api_url_file` keeps the credential in one place with one
# reader.
resource "kubernetes_secret" "alerting_webhook" {
  metadata {
    name      = "alerting-webhook"
    namespace = kubernetes_namespace.monitoring.metadata[0].name
  }

  # The key is the filename Alertmanager reads: the operator mounts a secret at
  # /etc/alertmanager/secrets/<secret name>/<key>, and the path in
  # clusters/management/infrastructure/controllers/kube-prometheus-stack.yaml
  # has to match this exactly. Two halves of one path in two files is a real
  # cost; the alternative is the credential in git.
  data = {
    webhook_url = local.alerting.webhook_url
  }
}
