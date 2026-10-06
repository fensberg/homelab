# The platform, planned on its own: against a config fixture, with no cluster
# behind it.
#
# That it plans at all is the property the split exists for. Nothing here
# reads a resource of the cluster's, so a plan needs nothing from it but the
# kubeconfig the Flux bootstrap hands to kubectl - and with the provider
# mocked, not even that has to be real.
#
# The fixture is the cluster module's: both read one config, and a second
# corpus would be a second thing to keep valid.

mock_provider "kubernetes" {}

variables {
  site           = "site0"
  config_path    = "../cluster/tests/fixtures/valid.json"
  kubeconfig     = "not a kubeconfig"
  site_directory = false

  # Applications written for the test, so what is asserted of the mechanism
  # holds whichever applications the estate has. The fixture gives the site
  # one of them, alpha, in staging.
  applications_path = "../applications/tests/fixtures/declared"
}

run "the_platform_plans_from_the_clusters_access_alone" {
  command = plan

  assert {
    condition     = kubernetes_namespace.flux_system.metadata[0].name == "flux-system"
    error_message = "the platform root no longer creates Flux's namespace, so the bootstrap applies into nothing"
  }
  assert {
    condition     = kubernetes_secret.cluster_vars.data.GITOPS_PATH == "clusters/core" && kubernetes_secret.cluster_vars.data.SITE == "site0"
    error_message = "a site with no directory of its own is not told to reconcile the core, or not told which site it is"
  }
  assert {
    condition     = output.state_db_endpoint == "${local.node_ips[0]}:${local.state_db_nodeport}" && local.node_ips[0] != ""
    error_message = "the state database's endpoint is not the first control plane's address and the address plan's port"
  }
}

# A site that has been given work reconciles its own directory, which builds
# on the core.
run "a_site_with_a_directory_reconciles_it" {
  command = plan

  variables {
    site_directory = true
  }

  assert {
    condition     = kubernetes_secret.cluster_vars.data.GITOPS_PATH == "clusters/site0"
    error_message = "a site with a directory of its own is not told to reconcile it"
  }
}

# The same validation the cluster module makes, and it has to be made here too:
# this module indexes the same map, and a mistyped site would otherwise fail on a
# raw "Invalid index" naming a line of variables.tf.
run "unknown_site_fails_its_precondition" {
  command = plan

  variables {
    site = "not-a-site"
  }

  expect_failures = [var.site]
}

# An application the site was given gets its namespace, every Secret it
# declared - each key from the one source it named - and an identity for Flux
# that may manage that namespace and nothing else. One the site was not given
# gets nothing, though it is declared.
#
# Read through locals rather than by keyed address: a resource address with a
# name in it is what a plan prints, and tests/go/repo refuses one written down.
run "a_given_application_is_provided_what_it_declared" {
  command = plan

  assert {
    condition     = output.applications == tolist(["alpha"]) && keys(kubernetes_namespace.application) == ["alpha"]
    error_message = "the site's namespaces are not exactly those of the applications it was given"
  }
  assert {
    condition = alltrue([
      for name, ns in kubernetes_namespace.application :
      ns.metadata[0].name == name && ns.metadata[0].labels["homelab/workload"] == name && ns.metadata[0].labels["pod-security.kubernetes.io/enforce"] == "restricted"
    ])
    error_message = "an application's namespace is not named for it, is not marked as one, or is not held to the restricted pod security standard"
  }
  assert {
    condition     = keys(kubernetes_secret.application) == ["alpha.alpha-backup", "alpha.alpha-keys"]
    error_message = "the Secrets created are not exactly those the site's applications declared"
  }
  assert {
    condition     = alltrue([for key, s in kubernetes_secret.application : "${s.metadata[0].namespace}.${s.metadata[0].name}" == key])
    error_message = "a declared Secret is not created under its own name in its application's namespace"
  }
  assert {
    condition = jsonencode({ for s in values(kubernetes_secret.application) : s.metadata[0].name => nonsensitive(s.data) }) == jsonencode({
      alpha-backup = {
        bucket   = "example-site0-staging"
        endpoint = "https://fixture-account-id.r2.cloudflarestorage.com"
        id       = "11112222333344445555666677778888"
        key      = "fixture-backup-key"
        secret   = "fixture-secret-access-key"
      }
      alpha-keys = {
        kind = "s3"
        user = "fixture-user"
      }
    })
    error_message = "a key does not carry the value of the one source it named: a vault field, a generated one, a fact about the bucket of the environment the site runs the application in, or the value written in the declaration"
  }
  assert {
    condition = alltrue([
      for name, sa in kubernetes_service_account.application_reconciler :
      sa.metadata[0].name == "application-${name}" && sa.metadata[0].namespace == "flux-system"
    ]) && keys(kubernetes_service_account.application_reconciler) == ["alpha"]
    error_message = "the identity Flux reconciles an application as is not where a Kustomization looks for it"
  }
  assert {
    condition = alltrue([
      for name, rb in kubernetes_role_binding.application_reconciler :
      rb.metadata[0].namespace == name && rb.role_ref[0].kind == "ClusterRole" && rb.role_ref[0].name == "admin" &&
      rb.subject[0].kind == "ServiceAccount" && rb.subject[0].name == "application-${name}" && rb.subject[0].namespace == "flux-system"
    ]) && keys(kubernetes_role_binding.application_reconciler) == ["alpha"]
    error_message = "an application's reconciler is not bound to its own namespace, and to that alone"
  }
  assert {
    condition     = kubernetes_secret.cluster_vars.data.ADDRESS_FRONT_DOOR != "" && kubernetes_secret.cluster_vars.data.ADDRESS_SIDE_DOOR != ""
    error_message = "a declared route's address is not handed to Flux"
  }
}

# A site given an application the pinned commit does not have is refused,
# rather than given a namespace with none of what the application needs.
run "an_application_the_pinned_commit_lacks_is_refused" {
  command = plan

  variables {
    applications_path = "../applications/tests/fixtures/other"
  }

  expect_failures = [kubernetes_namespace.application]
}

# The storage driver is told to verify who it is talking to, by the name the
# hypervisor's certificate carries, and to keep its volumes in the site's own
# storage - and it is handed the authority to verify against.
run "the_storage_driver_verifies_the_hypervisor_and_keeps_to_its_own_storage" {
  command = plan

  assert {
    condition = alltrue([
      for c in yamldecode(kubernetes_secret.storage_driver.data["config.yaml"]).clusters :
      c.insecure == false && c.url == "https://fixture-hv0:8006/api2/json" && c.region == "site0"
    ])
    error_message = "the storage driver is told to skip verification, or is not sent to the hypervisor by the name its certificate carries, or not told which site it serves"
  }
  assert {
    condition     = kubernetes_secret.storage_driver.binary_data["authority.crt"] == "fixture+authority+++"
    error_message = "the storage driver is not handed the hypervisor's own authority, so it has nothing to verify against"
  }
  assert {
    condition     = kubernetes_secret.storage_vars.data.VOLUME_STORAGE == "site0-volumes" && kubernetes_secret.storage_vars.data.HYPERVISOR_ADDRESS == "10.10.0.5"
    error_message = "the driver's manifests are not told the site's own volume storage, or where the hypervisor is"
  }
}
