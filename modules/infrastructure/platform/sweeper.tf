# What puts an untrusted machine back as it was built: what it needs before
# Flux runs it.
#
# An untrusted worker runs whatever a pull request says to run,
# and the sweeper returns it to the snapshot the converge took once that work
# is finished (docs/epochs/08-agent-roles.md). To do that it holds a token
# for the hypervisor's API inside the cluster. What the token reaches is
# decided on the hypervisor (management/hypervisor/hypervisor-prep.yml): the
# site's untrusted pool and nothing else, to stop, put back, start and
# delete a machine there.
#
# Flux runs the sweeper; this is what Flux cannot create. Its namespace, and
# in it the token, which comes from the vault, with the hypervisor's own
# authority so the sweeper verifies who it is talking to.
#
# A SITE WITH NO UNTRUSTED ZONE HAS NONE OF THIS. The hypervisor makes the
# token only where there is such a zone, so the config names one only there,
# and a site whose config names none is given no namespace and no secret.

locals {
  sweeper = try(local.site.hypervisor.sweeper, null)
}

resource "kubernetes_namespace" "sweeper" {
  count = local.sweeper == null ? 0 : 1

  metadata {
    name = "sweeper"

    labels = {
      # One program that makes requests and touches no disk of its machine.
      "pod-security.kubernetes.io/enforce" = "restricted"
    }
  }
}

resource "kubernetes_secret" "sweeper" {
  count = local.sweeper == null ? 0 : 1

  metadata {
    # The namespace already says whose; this says what it is.
    name      = "hypervisor"
    namespace = kubernetes_namespace.sweeper[0].metadata[0].name
  }

  data = {
    # By name and not by address: the certificate names the host, and Flux
    # gives the sweeper's pod an entry that says where that name is.
    HYPERVISOR_URL          = "https://${local.storage_hypervisor.hostname}:8006/api2/json"
    HYPERVISOR_NODE         = local.storage_hypervisor.hostname
    HYPERVISOR_TOKEN_ID     = local.sweeper.token_id
    HYPERVISOR_TOKEN_SECRET = local.sweeper.token_secret
    # Still encoded, as the vault keeps it on one line: the sweeper reads it
    # that way, and decoding it here would have OpenTofu hold it as text.
    HYPERVISOR_AUTHORITY = local.storage_driver.authority
  }
}
