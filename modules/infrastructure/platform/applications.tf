# What an application cannot create for itself: its namespace, its Secrets,
# and the identity Flux reconciles it as.
#
# Same rule as everything else OpenTofu makes here: it creates only what Flux
# cannot. Flux reconciles an application's Deployment, policy and volume from
# its release; it cannot invent a password, and it must not read one out of a
# manifest. So the values come from the vault, through the rendered config,
# into a Kubernetes Secret - the same path the state database's password and
# the runner's App credentials already take.
#
# NOTHING HERE NAMES AN APPLICATION. Which applications this site runs is the
# site's directory in the Flux tree, which the contractor reads into the
# rendered config (sites.<site>.applications) together with the fields of
# each one's vault item. Which Secrets an application needs, and where each
# key's value comes from, the application declares in its own directory
# (modules/infrastructure/applications). This file provides whatever is
# declared, for whichever applications the site was given.
#
# THE NAMESPACE IS HERE RATHER THAN IN THE APPLICATION'S MANIFESTS, AND HAS TO
# BE. A Secret needs a namespace to live in, and OpenTofu runs before Flux has
# reconciled anything - it is what installs Flux. A namespace declared only in
# git would not exist yet at the moment its Secret is written. An application
# therefore does not declare its own, which is the same split runner.tf and
# database.tf already use.

locals {
  # The applications this site was given: name => the environment it runs it
  # in, and its vault item's fields.
  applications = try(local.site.applications, {})

  # What an application's Secret may be told about its bucket: the one the
  # estate granted the site for the environment the site runs it in, and the
  # credential scoped to that bucket alone. An application in staging holding
  # this cannot reach production's data, and the state and database buckets
  # are out of its reach entirely.
  application_storage = {
    for name, a in local.applications : name => {
      bucket            = local.object_storage[a.environment].bucket
      access_key_id     = local.object_storage[a.environment].access_key_id
      secret_access_key = local.object_storage[a.environment].secret_access_key
      endpoint          = local.object_storage_endpoint
    }
  }

  # The declared Secrets of the applications this site was given.
  application_secrets = {
    for key, s in module.applications.secrets : key => s
    if contains(keys(local.applications), s.application)
  }

  # The identity Flux reconciles an application as. In Flux's own namespace,
  # because that is where a Kustomization looks for the service account it
  # names.
  application_reconciler_prefix = "application-"
}

resource "kubernetes_namespace" "application" {
  for_each = toset(keys(local.applications))

  metadata {
    # An application's directory names its namespace, so nothing has to say
    # which namespace is whose.
    name = each.key

    labels = {
      # What marks a namespace as an application's. A policy that has to
      # reach whatever applications the site runs selects on this, rather
      # than on any one of their names.
      "homelab/workload" = each.key
      # The same, under the key it had: one that carried the name of the
      # organisation this repository was first built for, which a fork would
      # have inherited as its own (#540). Kept until every site runs a
      # release that sets the key above, because the policy that selects on
      # it is reconciled from main and a namespace is labelled by a release;
      # then this line and the selector that reads it go together.
      "homelab.fensberg.com/workload" = each.key

      # Restricted: no privilege escalation, no host namespaces, a non-root
      # user, a seccomp profile. It closes the escape primitives that are the
      # second step in the only viable path to the runner's credentials.
      "pod-security.kubernetes.io/enforce"         = "restricted"
      "pod-security.kubernetes.io/enforce-version" = "latest"
    }
  }

  lifecycle {
    # Flux does not manage this namespace, but it does label things it
    # adopts. Ownership of anything it adds belongs to it rather than to a
    # plan that would strip it on every run.
    ignore_changes = [metadata[0].labels["kustomize.toolkit.fluxcd.io/name"]]

    # The site's roots read their modules, and the applications beside them,
    # as they were at the commit the site is pinned to. A site given an
    # application that commit does not have would get a namespace with none
    # of the Secrets the application needs.
    precondition {
      condition     = contains(module.applications.names, each.key)
      error_message = "This site was given the application ${each.key}, and the commit the site is pinned to has no such application. Move the site's pin to a commit that has it."
    }
  }
}

# Every Secret an application declares, with each key from the one source its
# declaration names: a field of its vault item, a fact about its bucket, or a
# value written in the declaration because it is no secret but is read from
# the same place as the rest.
#
# An application cannot name a vault item, a bucket or a namespace - only a
# field, a fact and a Secret's name - so what it can be given is its own and
# nothing else's, whatever its declaration says.
resource "kubernetes_secret" "application" {
  for_each = local.application_secrets

  metadata {
    name      = each.value.name
    namespace = kubernetes_namespace.application[each.value.application].metadata[0].name
  }

  data = {
    # The first of these that the declaration has. A key whose source names
    # something this site was not given - a vault field the rendered config
    # lacks, a fact a bucket does not have - matches none and fails the plan.
    for key, source in each.value.keys : key => try(
      source.value,
      local.application_storage[each.value.application][source.storage],
      local.applications[each.value.application].vault[source.vault],
      local.applications[each.value.application].vault[source.generated],
    )
  }
}

# The identity Flux reconciles each application as, and all it may do: manage
# what is in the application's own namespace.
#
# Flux's own identity is cluster-admin, so a Kustomization reconciled as Flux
# can create anything anywhere - another application's objects, a
# ClusterRole, a change to the core. An application's Kustomization names
# this service account instead (tests/go/repo holds every site's block to
# it), and its release is then applied with these rights: a manifest that
# reaches outside the application's namespace is refused by the API server,
# not by a reviewer.
resource "kubernetes_service_account" "application_reconciler" {
  for_each   = toset(keys(local.applications))
  depends_on = [kubernetes_namespace.flux_system]

  metadata {
    name      = "${local.application_reconciler_prefix}${each.key}"
    namespace = "flux-system"
  }
}

resource "kubernetes_role_binding" "application_reconciler" {
  for_each = toset(keys(local.applications))

  metadata {
    name      = "flux-reconciler"
    namespace = kubernetes_namespace.application[each.key].metadata[0].name
  }

  # The built-in role for running a namespace: its workloads, Services,
  # volumes, policies and service accounts, and nothing cluster-wide.
  role_ref {
    api_group = "rbac.authorization.k8s.io"
    kind      = "ClusterRole"
    name      = "admin"
  }

  subject {
    kind      = "ServiceAccount"
    name      = kubernetes_service_account.application_reconciler[each.key].metadata[0].name
    namespace = "flux-system"
  }
}

# An output so a plan says when it changes - which is the moment a site is
# given an application, or has one taken away.
output "applications" {
  description = "The applications this site was given a namespace for."
  value       = sort(keys(local.applications))
}
