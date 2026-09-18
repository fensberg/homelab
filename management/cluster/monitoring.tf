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
