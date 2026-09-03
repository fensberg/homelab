resource "talos_machine_secrets" "this" {
  talos_version = local.talos_version
}

data "talos_machine_configuration" "controlplane" {
  for_each           = local.control_plane
  cluster_name       = local.cluster_name
  machine_type       = "controlplane"
  cluster_endpoint   = "https://${local.node_ips[0]}:6443"
  machine_secrets    = talos_machine_secrets.this.machine_secrets
  kubernetes_version = local.kubernetes_version

  # Every network-related concern here uses the modern multi-document config
  # types (HostnameConfig / LinkConfig / ResolverConfig), not the monolithic
  # `machine.network.*` fields. Confirmed directly from source
  # (pkg/machinery/config/types/v1alpha1/v1alpha1_types.go, this pinned tag):
  # "NetworkConfig represents the machine's networking config values. //
  # Deprecated: all fields in NetworkConfig are deprecated, use corresponding
  # multi-doc config types instead." An earlier version of this file used the
  # deprecated fields for addressing and nameservers - they produced no error
  # anywhere in the pipeline, generation or apply, but the address never
  # actually took effect on the node.
  #
  # No `machine.install` section, on purpose. compute.tf builds these VMs
  # from a pre-installed Talos disk image rather than an installer ISO -
  # Talos's own install process reliably corrupted the node's network config
  # the moment it ran (full diagnosis in the epoch record's "Blocked"
  # section). There is nothing left for install to do here, so there is
  # nothing to configure it with.
  config_patches = [
    yamlencode({
      cluster = {
        allowSchedulingOnControlPlanes = true
      }
    }),
    yamlencode({
      apiVersion = "v1alpha1"
      kind       = "LinkConfig"
      name       = "eth0"
      addresses  = [{ address = "${each.value.ip}/24" }]
      # destination omitted: a default route is created for the gateway's
      # address family when none is specified (RouteConfig's own doc
      # comment, same source file as LinkConfig).
      routes = [{ gateway = local.node_gateway }]
    }),
    yamlencode({
      apiVersion  = "v1alpha1"
      kind        = "ResolverConfig"
      nameservers = [{ address = "1.1.1.1" }, { address = "1.0.0.1" }]
    }),
    # Join the node to the overlay.
    #
    # This is how the cluster reaches the hypervisor at all. The node subnet
    # lives in an EVPN VRF, and a VRF cannot deliver to a local address in
    # another VRF - the hypervisor's management address is the host itself, and
    # delivery is decided by which VRF the listening socket is bound to, so no
    # amount of routing makes it reachable from a pod. As a tailnet peer the
    # node reaches it directly, and the tailnet ACL rather than a subnet decides
    # what it may touch.
    #
    # The extension comes from the schematic; this only configures it. Both
    # halves have to move together, which is why the schematic id and this
    # patch are in the same change.
    yamlencode({
      apiVersion = "v1alpha1"
      kind       = "ExtensionServiceConfig"
      name       = "tailscale"
      environment = [
        "TS_AUTHKEY=${tailscale_tailnet_key.nodes.key}",
        # Tagged, not user-owned: a tagged device does not expire, so a node
        # does not silently drop off the tailnet 90 days after it was built.
        "TS_EXTRA_ARGS=--advertise-tags=${local.overlay_node_tag}",
        # Nodes consume routes; they never advertise any. The hypervisor is
        # the subnet router for this site and giving a node that job as well
        # would put two advertisers on one prefix.
        "TS_ROUTES=",
      ]
    }),
    # The generator already emits a HostnameConfig document itself, with
    # `auto: stable` set, and config_patches merge field-by-field rather
    # than replacing the whole document - so adding `hostname` alone left
    # both fields set. Per the actual validation in
    # pkg/machinery/config/types/network/hostname.go (siderolabs/talos,
    # this pinned tag), the two are not really mutually exclusive: the
    # check only fires when auto is anything other than exactly "off".
    # `auto = false` (an earlier attempt) isn't a valid enum member at all -
    # the only accepted values are "stable" and "off".
    yamlencode({
      apiVersion = "v1alpha1"
      kind       = "HostnameConfig"
      auto       = "off"
      hostname   = each.value.name
    }),
    # the storage provisioner's own data path (see compute.tf's second disk on each node).
    # !system_disk is Talos's documented selector for "whichever disk this
    # node did not boot from" - confirmed from pkg/machinery/cel/celenv
    # (this pinned tag) rather than matching on a specific size or device
    # path, which would break the moment a disk is resized or reordered.
    # UserVolumeConfig always mounts at /var/mnt/<name>, so this lands at
    # /var/mnt/storage with no separate mount path to configure.
    yamlencode({
      apiVersion = "v1alpha1"
      kind       = "UserVolumeConfig"
      name       = "storage"
      provisioning = {
        diskSelector = {
          match = "!system_disk"
        }
        # Talos's apiserver rejects a UserVolumeConfig with neither bound
        # set ("min size or max size is required") - the provider's own
        # example omits both, so this was only caught by a real apply, not
        # by anything client-side. minSize is set low on purpose: it only
        # needs to comfortably undershoot the dedicated 32Gi disk in
        # compute.tf, not describe it exactly. grow claims the rest of the
        # disk rather than stopping at the minimum.
        minSize = "1GiB"
        grow    = true
      }
      filesystem = {
        type = "xfs"
      }
    })
  ]
}

resource "talos_machine_configuration_apply" "control_plane" {
  for_each             = local.control_plane
  depends_on           = [proxmox_virtual_environment_vm.talos_cp]
  client_configuration = talos_machine_secrets.this.client_configuration

  machine_configuration_input = data.talos_machine_configuration.controlplane[each.key].machine_configuration
  node                        = each.value.ip
}

resource "talos_machine_bootstrap" "this" {
  depends_on           = [talos_machine_configuration_apply.control_plane]
  client_configuration = talos_machine_secrets.this.client_configuration
  node                 = local.node_ips[0]
}

resource "talos_cluster_kubeconfig" "this" {
  depends_on           = [talos_machine_bootstrap.this]
  client_configuration = talos_machine_secrets.this.client_configuration
  node                 = local.node_ips[0]
}

data "talos_client_configuration" "this" {
  cluster_name         = local.cluster_name
  client_configuration = talos_machine_secrets.this.client_configuration
  endpoints            = [local.node_ips[0]]
}

# talos_machine_bootstrap returning doesn't mean the Kubernetes API is
# actually reachable yet - etcd forming and kube-apiserver becoming ready
# both happen asynchronously after bootstrap. Every kubernetes_* resource in
# this configuration depends on this instead of talos_cluster_kubeconfig
# directly, so they wait for a real, checked "the API answers" rather than
# racing it and hitting connection refused.
# The gate that stops Flux, the database and the runner being applied before
# the cluster can carry them. Nothing reads a value from it: its whole job is
# the four depends_on edges pointing at it from database.tf, gitops.tf and
# runner.tf.
#
# The dependency on the VMs is what makes it usable rather than obstructive.
#
# control_plane_nodes is local.node_ips - the nodes the *config* asks for -
# and that is fully known at plan time. Without a dependency on the machines
# themselves, raising control_plane_count made this read against addresses
# with no machine behind them, wait, and time out after ten minutes producing
# no plan at all. The same scoping made a destroy ask whether a three-node
# cluster was a healthy five-node one, which can never be true, and spend the
# same ten minutes finding out.
#
# OpenTofu defers a data source read to apply time when something it depends
# on has pending changes. Depending on the VMs therefore means:
#
#   scale change   VMs pending -> read deferred -> the plan completes and
#                  shows the machines being added
#   steady state   nothing pending -> read happens -> passes as before
#   ignition       VMs pending -> deferred to apply -> the gate still fires
#                  before Flux, which is the only moment it matters
#   destroy        VMs pending deletion -> deferred -> no ten-minute wait
#
# The gate is preserved in every case that needs one, because deferring to
# apply is precisely when it should run. It was never the plan's job to
# answer whether a cluster is healthy.
data "talos_cluster_health" "this" {
  depends_on = [
    talos_cluster_kubeconfig.this,
    proxmox_virtual_environment_vm.talos_cp,
  ]
  client_configuration = talos_machine_secrets.this.client_configuration
  control_plane_nodes  = local.node_ips
  endpoints            = local.node_ips

  timeouts = {
    read = "10m"
  }
}
