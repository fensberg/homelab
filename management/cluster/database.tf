# =============================================================================
# State database plumbing. Vendor: CloudNativePG (deployed by Flux, not here).
#
# OpenTofu creates only the things Flux cannot: the namespace and the secrets.
# The operator and the Cluster resource itself are declared in git under
# clusters/management/ and reconciled by Flux.
#
# WHY SECRETS COME FROM HERE
# --------------------------
# Flux reconciles from git, so a password cannot live in a manifest. The usual
# answers are SOPS or External Secrets, both of which are their own epoch of
# work. Until then OpenTofu writes the secrets directly - it already holds the
# 1Password-rendered values, and ignition is a local, human-run operation.
# =============================================================================

resource "kubernetes_namespace" "database" {
  depends_on = [talos_cluster_kubeconfig.this]

  metadata {
    name = local.state_db_namespace
  }
}

resource "kubernetes_secret" "state_db_credentials" {
  metadata {
    name      = "${local.state_db_cluster}-app"
    namespace = kubernetes_namespace.database.metadata[0].name
  }

  type = "kubernetes.io/basic-auth"

  data = {
    username = local.state_db_owner
    password = local.site_state.db_password
  }
}

# Credentials the database uses to write its own WAL archive and base backups
# to object storage. These never leave the cluster.
#
# LEAST PRIVILEGE: these need Object Read & Write, scoped to this bucket only.
# The Postgres pod writes the backups itself, so it needs write - read alone
# would break WAL archiving. It never creates or deletes buckets, so it must
# not carry admin scope. Retention pruning is a DELETE on objects, which
# object-level write already covers.
#
# This is the credential that lives in the cluster indefinitely, so it is the
# one worth tightening hardest. The separate admin token in versions.tf exists
# only to create the bucket and is wiped after ignition.
resource "kubernetes_secret" "object_storage_credentials" {
  metadata {
    name      = "object-storage-credentials"
    namespace = kubernetes_namespace.database.metadata[0].name
  }

  data = {
    ACCESS_KEY_ID     = local.object_storage.access_key_id
    SECRET_ACCESS_KEY = local.object_storage.secret_access_key
  }
}

# Non-secret-but-not-in-git values that the Flux Kustomizations substitute into
# the manifests at reconcile time (postBuild.substituteFrom). This is how the
# bucket name and account-specific endpoint stay out of the repository.
resource "kubernetes_secret" "cluster_vars" {
  depends_on = [flux_bootstrap_git.this]

  metadata {
    name      = "cluster-vars"
    namespace = "flux-system"
  }

  data = {
    OBJECT_STORAGE_BUCKET   = local.object_storage.bucket
    OBJECT_STORAGE_ENDPOINT = "https://${local.object_storage.account_id}.r2.cloudflarestorage.com"
    STATE_DB_NAMESPACE      = local.state_db_namespace
    STATE_DB_CLUSTER        = local.state_db_cluster
    STATE_DB_NAME           = local.state_db_name
    STATE_DB_OWNER          = local.state_db_owner
    STATE_DB_NODEPORT       = tostring(local.state_db_nodeport)
  }
}

output "state_db_endpoint" {
  description = "Host and port the state database is reachable on from outside the cluster."
  value       = "${local.node_ips[0]}:${local.state_db_nodeport}"
}

# The connection string is derived, not stored. Every component is already
# known before the database exists: the owner, database name and NodePort are
# declared in variables.tf, the address falls out of the site registry, and the
# password comes from 1Password. Keeping it as a separate vault item would
# invent a chicken-and-egg problem - you cannot record a connection string for
# a database that has not been created yet - for no benefit.
output "state_conn_str" {
  description = "Connection string for the OpenTofu pg backend."
  sensitive   = true
  value = format(
    "postgres://%s:%s@%s:%d/%s?sslmode=require",
    local.state_db_owner,
    urlencode(local.site_state.db_password),
    local.node_ips[0],
    local.state_db_nodeport,
    local.state_db_name,
  )
}
