# =============================================================================
# Self-hosted CI runners. Vendor: Actions Runner Controller (deployed by Flux,
# not here).
#
# Same division of labour as the state database: OpenTofu creates only what
# Flux cannot hold - the namespaces and the credential - and the controller and
# the scale set are declared in clusters/management/ and reconciled from git.
#
# WHY THE CREDENTIAL IS SCOPED THE WAY IT IS
# ------------------------------------------
# The App this authenticates as holds Organization Self-hosted runners
# (read/write) and Metadata (read). It deliberately does not hold repository
# Administration, which would have been the requirement for a repository-level
# scale set - and which includes branch protection, the mechanism that makes
# "the agent proposes, the human disposes" true.
#
# That is the fail-closed invariant applied rather than restated. This key
# lives in a cluster indefinitely, so the question worth answering was not how
# well it is guarded but what it can do once it is not: with this scope the
# worst use of a stolen copy is registering a runner nobody asked for or
# deleting one that exists. Both are visible in the organization's runner list
# and both are undone by deleting a row.
# =============================================================================

# Two namespaces, not one. The controller holds the App credential; the runners
# execute whatever a workflow tells them to. Separating them means a
# compromised job is not in the same namespace as the key, which is what makes
# the low likelihood in the epoch record's threat model actually low rather
# than merely asserted.
resource "kubernetes_namespace" "runner_system" {
  depends_on = [data.talos_cluster_health.this]

  metadata {
    name = local.runner_system_namespace
  }
}

resource "kubernetes_namespace" "runners" {
  depends_on = [data.talos_cluster_health.this]

  metadata {
    name = local.runners_namespace
  }
}

# The field names are ARC's, not ours: the controller looks for exactly these
# three keys. Named by what they are rather than renamed to fit this project's
# vocabulary, because a key a third party reads by name is that third party's
# interface.
resource "kubernetes_secret" "runner_app_credentials" {
  metadata {
    name      = local.runner_secret_name
    namespace = kubernetes_namespace.runner_system.metadata[0].name
  }

  data = {
    github_app_id              = local.runner.app_id
    github_app_installation_id = local.runner.installation_id
  }

  # binary_data, not data, and not base64decode() either. The config carries
  # the PEM base64-encoded so it survives `op inject` substituting it into a
  # JSON template - and binary_data is the field that takes an already-encoded
  # value, storing the decoded bytes. So the encoding introduced for transport
  # is undone by the API that wanted it that way anyway, and no HCL expression
  # ever holds the decoded key.
  #
  # It also keeps `tofu validate` working against the placeholder config, where
  # this value is a stand-in rather than a real encoded key.
  binary_data = {
    github_app_private_key = local.runner.private_key
  }
}

# The runners need the same credential to register themselves, in their own
# namespace. A second copy rather than a shared one: a namespace boundary that
# is crossed by a mounted secret is not a boundary.
#
# This is the copy a compromised job could reach, and it is the reason the
# App's scope matters more than its storage. Reading it buys the ability to
# register runners, which is what the pod holding it was already doing.
resource "kubernetes_secret" "runner_app_credentials_runners" {
  metadata {
    name      = local.runner_secret_name
    namespace = kubernetes_namespace.runners.metadata[0].name
  }

  data = {
    github_app_id              = local.runner.app_id
    github_app_installation_id = local.runner.installation_id
  }

  binary_data = {
    github_app_private_key = local.runner.private_key
  }
}

# The one substitution value the Flux-reconciled manifests need, in its own
# secret rather than added to cluster-vars. Two secrets, two owners: runner.tf owns
# what the runner manifests need and database.tf owns what the database
# manifests need, so neither file has to be edited to change the other's
# variables. infra-configs.yaml lists both.
resource "kubernetes_secret" "runner_vars" {
  metadata {
    name      = "runner-vars"
    namespace = "flux-system"
  }

  # Two keys, not seven. Everything else the manifests need is constant and is
  # written literally there, where it is legible without cross-referencing a
  # secret. These two are not: the config URL is derived from a vault secret
  # and cannot be committed, and the credential's name is substituted so that
  # it and the secret created above cannot drift - and, incidentally, so that
  # checkov stops reading a hyphenated identifier as a leaked credential.
  data = {
    RUNNER_GITHUB_CONFIG_URL = local.runner_github_config_url
    RUNNER_SECRET_NAME       = local.runner_secret_name
  }
}
