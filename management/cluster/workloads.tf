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

  # ONE NAME BY DEFAULT, TWO WHEN THEY HAVE TO DIFFER.
  #
  # These were two vault items, then one, and are now one with an override. The
  # round trip is worth recording because both ends were wrong for the same
  # reason - each was a guess about whether the listing name and the world name
  # are one thing.
  #
  # Two items drifted, because keeping them equal was something to remember: the
  # listing advertised a generated name over a world called something else.
  # Collapsing them fixed that and removed a lever nobody had needed yet - and
  # the first time one was needed, the only way to change the listing name was
  # to change the world name, which does not rename a world but abandons it.
  #
  # They are not one thing. The world name is welded to a file on disk. The
  # listing name is free. An empty server_name means "the same as the world",
  # so the common case needs no maintenance and the lever is there when it is.
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
    server-name = local.valheim.server_name != "" ? local.valheim.server_name : local.valheim.name
    world-name  = local.valheim.name
    password    = local.valheim.password
  }
}

# What the world's backups need: the production bucket, a credential scoped to
# it, and the key they are encrypted with.
#
# The backup sidecar and the restore init container in the game server's pod
# both read this, and nothing else in the pod does - the server itself never
# mounts it. rclone is configured entirely from the environment: the remote
# "r2" is the bucket, and the scripts wrap it in an encrypting remote keyed
# from WORLD_BACKUP_KEY, so the bucket holds ciphertext.
#
# The production bucket's own credential, not the estate's: a staging workload
# holding this cannot reach production's data, and the state and database
# buckets are out of its reach entirely.
#
# No preconditions here, because both failures are already refused earlier: a
# site without a production credential fails the config's own validation, and
# the Render phase generates the backup key before anything reads it. Were
# either to slip through, the restore init container cannot read the bucket
# and refuses to start the server rather than starting a fresh world.
resource "kubernetes_secret" "valheim_backup" {
  metadata {
    name      = "valheim-backup"
    namespace = kubernetes_namespace.valheim.metadata[0].name
  }
  data = {
    RCLONE_CONFIG_R2_TYPE              = "s3"
    RCLONE_CONFIG_R2_PROVIDER          = "Cloudflare"
    RCLONE_CONFIG_R2_ACCESS_KEY_ID     = local.object_storage.production.access_key_id
    RCLONE_CONFIG_R2_SECRET_ACCESS_KEY = local.object_storage.production.secret_access_key
    RCLONE_CONFIG_R2_ENDPOINT          = local.object_storage_endpoint
    # The token is scoped to one bucket and cannot list or create buckets, so
    # rclone must not try.
    RCLONE_CONFIG_R2_NO_CHECK_BUCKET = "true"
    WORLD_BACKUP_BUCKET              = cloudflare_r2_bucket.production.name
    WORLD_BACKUP_KEY                 = local.valheim.backup_key
  }
}
