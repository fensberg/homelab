# The platform root, planned on its own: against a config fixture, with the
# cluster's access as an input and no cluster behind it.
#
# That it plans at all is the property the split exists for. This root's
# provider is configured from variables, so a plan needs nothing from the
# cluster root but two values - and with the provider mocked, not even those
# have to be real.
#
# The fixture is the cluster root's: both roots read one config, and a second
# corpus would be a second thing to keep valid.

mock_provider "kubernetes" {}

variables {
  site        = "site0"
  config_path = "../cluster/tests/fixtures/valid.json"
  # Encoded here rather than written encoded: none of these is a credential,
  # and a base64 literal beside the word "key" reads as one to a scanner.
  cluster_access = {
    host               = "https://192.0.2.10:6443"
    ca_certificate     = base64encode("not a certificate")
    client_certificate = base64encode("not a certificate")
    client_key         = base64encode("not a key")
  }
  kubeconfig = "not a kubeconfig"
}

run "the_platform_plans_from_the_clusters_access_alone" {
  command = plan

  assert {
    condition     = kubernetes_namespace.flux_system.metadata[0].name == "flux-system"
    error_message = "the platform root no longer creates Flux's namespace, so the bootstrap applies into nothing"
  }
  assert {
    condition     = output.state_db_endpoint == "${local.node_ips[0]}:${local.state_db_nodeport}" && local.node_ips[0] != ""
    error_message = "the state database's endpoint is not the first control plane's address and the address plan's port"
  }
}

# The same validation the cluster root makes, and it has to be made here too:
# this root indexes the same map, and a mistyped site would otherwise fail on a
# raw "Invalid index" naming a line of variables.tf.
run "unknown_site_fails_its_precondition" {
  command = plan

  variables {
    site = "not-a-site"
  }

  expect_failures = [var.site]
}
