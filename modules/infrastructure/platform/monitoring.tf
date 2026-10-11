# =============================================================================
# Monitoring plumbing. Vendor: Prometheus and Grafana, deployed by Flux.
#
# The same division as database.tf: OpenTofu creates only what Flux cannot -
# the namespace and the secret - and the stack itself is declared in git under
# clusters/core/ and reconciled by Flux.
#
# WHY THE NAMESPACE IS HERE RATHER THAN IN THE FLUX MANIFEST. A secret has to
# exist in a namespace, and OpenTofu creating one that Flux also declares is a
# race with two owners: Flux would prune or adopt it depending on which
# reconciled last. So the namespace belongs to whichever side creates the
# secret, which is this one - the same arrangement the state database has.
# =============================================================================
resource "kubernetes_namespace" "monitoring" {
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
  # clusters/core/infrastructure/controllers/kube-prometheus-stack.yaml
  # has to match this exactly. Two halves of one path in two files is a real
  # cost; the alternative is the credential in git.
  #
  # The heartbeat's two are here and not in a Secret of their own, on
  # purpose. Alertmanager mounts each Secret it is told to, and a pod told
  # to mount one that does not exist yet does not start: a site whose core
  # had moved ahead of its release would have no alerting at all, and
  # nothing left running to say so. A key missing from a Secret that is
  # there only fails the one notification that reads it.
  data = {
    webhook_url      = local.alerting.webhook_url
    heartbeat_url    = local.heartbeat.url
    heartbeat_secret = local.heartbeat.secret
  }
}

# Where Prometheus should look for etcd.
#
# etcd is not a pod on Talos - it is a system service the machine runs - so
# nothing in the cluster can be selected to find it, and the chart needs the
# addresses themselves. They are node addresses, which this repository keeps
# out of git, so they arrive the way every other address does: OpenTofu writes
# them into a secret and Flux substitutes them into the manifest.
#
# A secret of its own rather than an entry in cluster-vars, following the rule
# already stated there: one owner per secret, so adding a monitoring variable
# never means editing the file that owns the database's.
#
# JSON rather than a comma-separated list, because the value is substituted
# into a YAML list position and JSON's array syntax is valid YAML flow
# sequence. `["10.0.0.1","10.0.0.2"]` lands as a list; `10.0.0.1,10.0.0.2`
# would land as a string and the chart would try to scrape one host with a
# comma in its name.
resource "kubernetes_secret" "monitoring_vars" {
  depends_on = [kubernetes_namespace.flux_system]

  metadata {
    name      = "monitoring-vars"
    namespace = "flux-system"
  }

  data = {
    ETCD_ENDPOINTS = jsonencode(local.node_ips)
  }
}

# The host under the machines, as something Prometheus can scrape.
#
# Its exporter is installed by the hypervisor playbook and answers on the
# host's own address, over TLS, to a scraper that presents a certificate. Three
# things here, and Flux can make none of them: where it answers is an address
# this repository keeps out of git, and what the scraper presents is a
# credential.
#
# A Service with no selector and the hypervisors' addresses behind it, so the scrape in
# the Flux tree names the exporter and never an address. The name is the one
# in the exporter's certificate, which the contractor generates
# (scripts/contractor/internal/phases/secrets.go).
resource "kubernetes_service" "hypervisor" {
  metadata {
    name      = "hypervisor"
    namespace = kubernetes_namespace.monitoring.metadata[0].name
  }

  spec {
    cluster_ip = "None"

    port {
      name        = "metrics"
      port        = 9100
      target_port = 9100
    }
  }
}

resource "kubernetes_endpoint_slice_v1" "hypervisor" {
  metadata {
    name      = "hypervisor"
    namespace = kubernetes_namespace.monitoring.metadata[0].name

    labels = {
      "kubernetes.io/service-name" = kubernetes_service.hypervisor.metadata[0].name
    }
  }

  address_type = "IPv4"

  # Each hypervisor at its own address: the one the site already reaches the
  # Proxmox API at.
  endpoint {
    addresses = [for key in sort(keys(local.site.hypervisor.nodes)) : local.site.hypervisor.nodes[key].ip]
  }

  port {
    name         = "metrics"
    port         = 9100
    app_protocol = "https"
  }
}

# What Prometheus verifies the exporter against, and what it presents to it.
# The exporter's own key is not here and never leaves the vault and the host.
resource "kubernetes_secret" "host_metrics" {
  metadata {
    name      = "host-metrics"
    namespace = kubernetes_namespace.monitoring.metadata[0].name
  }

  # Each is a PEM block the vault keeps base64-encoded, which is the form a
  # Secret stores: handed over as it is, and decoded nowhere on the way.
  binary_data = {
    "authority.crt" = local.site.hypervisor.metrics.authority
    "scraper.crt"   = local.site.hypervisor.metrics.scraper_certificate
    "scraper.key"   = local.site.hypervisor.metrics.scraper_private_key
  }
}
