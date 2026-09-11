# The namespace and secret a workload cannot create for itself.
#
# Same rule as everything else OpenTofu makes here: it creates only what Flux
# cannot. Flux reconciles the Deployment, the policy and the volume from git;
# it cannot invent a password, and it must not read one out of a manifest.
#
# So the values come from the vault, through the rendered config, into a
# Kubernetes Secret - the same path the state database's password and the
# runner's App credentials already take.
#
# THE NAMESPACE IS HERE RATHER THAN IN THE FLUX BASE, AND HAS TO BE.
#
# A secret needs a namespace to live in, and OpenTofu runs before Flux has
# reconciled anything - it is what installs Flux. A namespace declared only in
# git would not exist yet at the moment this secret is written. The workload's
# base therefore does not declare it, which is the same split runner.tf and
# database.tf already use.

resource "kubernetes_namespace" "valheim" {
  # The cluster has to be real before anything is created in it. Without this
  # edge OpenTofu starts this in the first wave of the apply, against an API
  # server still starting its static pods, and the run dies on connection
  # refused - taking the whole ignition down with it, because a failed ignition
  # tears the estate back down. The secret below inherits the ordering by
  # reading its namespace from here. tests/go/repo/kubernetes_gate_test.go
  # refuses a kubernetes_* resource with no such edge.
  depends_on = [data.talos_cluster_health.this]

  metadata {
    name = "valheim"

    labels = {
      # The policy in the workload's base selects on this rather than on the
      # namespace's name, so a second workload can reuse the same policy shape.
      "homelab.fensberg.com/workload" = "valheim"

      # Restricted: no privilege escalation, no host namespaces, a non-root
      # user, a seccomp profile. The image needs none of what this forbids, so
      # it costs nothing and closes the escape primitives that are the second
      # step in the only viable path to the runner's credentials.
      "pod-security.kubernetes.io/enforce"         = "restricted"
      "pod-security.kubernetes.io/enforce-version" = "latest"
    }
  }

  # Flux does not manage this namespace, but it does label things it adopts.
  # Ownership of anything it adds belongs to it rather than to a plan that
  # would strip it on every run.
  lifecycle {
    ignore_changes = [metadata[0].labels["kustomize.toolkit.fluxcd.io/name"]]
  }
}

resource "kubernetes_secret" "valheim_server" {
  metadata {
    name      = "valheim-server"
    namespace = kubernetes_namespace.valheim.metadata[0].name
  }

  # TWO KEYS, ONE VALUE, AND THAT IS THE POINT.
  #
  # The server takes a name for the listing and a name for the save file as
  # separate arguments, so the Secret keeps them separate - the image should not
  # have to care that this estate happens to supply one value for both. If a
  # reason ever appears to split them, it is a config change and nothing here or
  # in the image moves.
  #
  # What collapsed is the SOURCE. Two vault items meant two things to keep in
  # step, and they promptly fell out of step: the server list advertised a
  # randomly generated name while the world on disk was called something else
  # entirely, which reads as a misconfiguration to everybody who looks at it.
  # One name is the honest model of what an operator actually wants.
  #
  # Neither is a credential - the listing name is broadcast to anyone who joins.
  # It lives in the vault for the reason site names do: this repository is meant
  # to be forkable, and a fork should pick its own by changing its config rather
  # than by editing a manifest.
  #
  # Keeping all three in one Secret also means the deployment reads them one
  # way. A pod half-configured from a ConfigMap and half from a Secret is two
  # things to check when the wrong world loads.
  data = {
    server-name = local.valheim.name
    world-name  = local.valheim.name
    password    = local.valheim.password
  }
}
