# Volumes that outlive a machine: what the storage driver needs before Flux
# installs it.
#
# The driver asks the hypervisor for a volume and attaches it to whichever
# worker runs the pod, so a worker's replacement detaches the volume and the
# next worker gets it back. To ask, it holds a token for the hypervisor's API
# inside the cluster. What that token reaches is decided on the hypervisor
# (management/hypervisor/hypervisor-prep.yml, section 10): the site's worker
# pool, a storage that holds only the driver's own volumes, and nothing else.
#
# Flux installs the driver; this is what Flux cannot create. The namespace,
# because the driver's part on each machine mounts disks and needs a
# privileged one. The token, because it comes from the vault. And the
# hypervisor's own authority, so the driver verifies who it is talking to
# and is never told to believe whatever answers.

locals {
  storage_driver = local.site.hypervisor.storage_driver

  # A Proxmox cluster answers for every one of its nodes on any of them, so
  # the driver is given one: the first, in key order, as everything else here
  # that needs "a node of this site" takes it.
  storage_hypervisor = local.site.hypervisor.nodes[sort(keys(local.site.hypervisor.nodes))[0]]

  # The storage the hypervisor's playbook makes for this site's volumes.
  volume_storage = "${var.site}-volumes"
}

resource "kubernetes_namespace" "storage_driver" {
  metadata {
    # Named for what runs in it, like every other name here, and not for
    # the vendor whose driver it is.
    name = "storage-driver"

    labels = {
      # The driver's part on each machine formats and mounts block devices,
      # which is a host mount and a privileged container. Its controller is
      # neither, and is held to the restricted profile by its own settings.
      "pod-security.kubernetes.io/enforce" = "privileged"
      "pod-security.kubernetes.io/audit"   = "restricted"
      "pod-security.kubernetes.io/warn"    = "restricted"
    }
  }
}

resource "kubernetes_secret" "storage_driver" {
  metadata {
    # The namespace already says whose; this says what it is.
    name      = "config"
    namespace = kubernetes_namespace.storage_driver.metadata[0].name
  }

  data = {
    # The driver's own configuration file. By name and not by address: the
    # certificate names the host, and Flux gives the driver's pods an entry
    # that says where that name is.
    "config.yaml" = yamlencode({
      clusters = [{
        url          = "https://${local.storage_hypervisor.hostname}:8006/api2/json"
        insecure     = false
        token_id     = local.storage_driver.token_id
        token_secret = local.storage_driver.token_secret
        region       = var.site
      }]
    })
  }

  # Kept base64-encoded on one line in the vault, as the playbook stores it,
  # and handed over still encoded: decoding it here would require OpenTofu to
  # hold it as text, which a certificate is and a stand-in for one is not.
  binary_data = {
    "authority.crt" = local.storage_driver.authority
  }
}

# What the driver's manifests need that is the site's own, where the Flux
# Kustomization that installs controllers substitutes from.
resource "kubernetes_secret" "storage_vars" {
  depends_on = [kubernetes_namespace.flux_system]

  metadata {
    name      = "storage-vars"
    namespace = "flux-system"
  }

  data = {
    HYPERVISOR_HOSTNAME = local.storage_hypervisor.hostname
    HYPERVISOR_ADDRESS  = local.storage_hypervisor.ip
    VOLUME_STORAGE      = local.volume_storage
  }
}
