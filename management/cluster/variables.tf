variable "site_index" {
  type        = number
  default     = 0
  description = <<-EOT
    Which entry in the config's sites[] array to deploy. Selection only -
    the site's identity comes from the octet it declares, not from its
    position. Set by the start button via TF_VAR_site_index.
  EOT
}

locals {
  config = jsondecode(file("../../config/management.rendered.json"))

  site      = local.config.sites[var.site_index]
  # Named for the octet rather than the array position, so a VM name lines
  # up with its address (site10-cp-01 lives at 10.10.10.100) and stays stable
  # if sites[] is ever reordered.
  site_name = "site${local.site.octet}"

  hypervisors = local.site.hypervisor.nodes

  # Per-site because two sites are two estates: separate hypervisors, separate
  # tailnets when the engagement calls for it, separate buckets, and separate
  # state databases. Sharing any of them means compromising one site reaches
  # the others.
  overlay_network = local.site.overlay_network
  object_storage  = local.site.object_storage
  site_state      = local.site.state
  node_count  = local.site.control_plane_count

  # --- addressing ----------------------------------------------------------
  # The octet is declared, not computed. Reading the config tells you the
  # site's network without doing arithmetic, retiring a site means leaving a
  # gap rather than renumbering, and reordering sites[] no longer silently
  # repoints an estate at someone else's network.
  #
  # The cost is that collisions become expressible, so registry.tf asserts
  # uniqueness across every site rather than relying on the schema.
  octet     = local.site.octet
  site_cidr = "10.${local.octet}.0.0/16"

  # Talos control plane. Infrastructure sits in .0.0/24 and a load-balancer
  # pool is reserved at .20.0/24 for epoch 02.
  node_cidr    = "10.${local.octet}.10.0/24"
  node_gateway = cidrhost(local.node_cidr, 1)
  node_ips     = [for i in range(local.node_count) : cidrhost(local.node_cidr, 100 + i)]

  # DHCP pool, disjoint from the static nodes above.
  dhcp_start = cidrhost(local.node_cidr, 50)
  dhcp_end   = cidrhost(local.node_cidr, 99)

  # --- placement -----------------------------------------------------------
  # Control-plane VMs are dealt round-robin across whatever hypervisors the
  # site has. One hypervisor puts all of them on it; three put one on each,
  # which is what makes the cluster survive losing a box. Appending a node to
  # sites[].hypervisor.nodes is all it takes here - but a multi-node Proxmox
  # cluster also needs a vxlan or evpn SDN zone, see docs/epochs/01-ignition.md.
  vm_placement = [
    for i in range(local.node_count) :
    local.hypervisors[i % length(local.hypervisors)].hostname
  ]

  # --- identity ------------------------------------------------------------
  # Everything nameable carries the site, so two sites are distinguishable at
  # a glance in Proxmox, in Talos and in kubeconfig.
  # site.name is a vault reference, so the human label for a site never
  # reaches git while still appearing in the cluster name.
  cluster_name = "${local.config.organization.name}-${local.site.name}"
  vm_names     = [for i in range(local.node_count) : format("%s-cp-%02d", local.site_name, i + 1)]

  # Banded by octet so two sites could share a Proxmox cluster without
  # colliding: octet 10 uses 1000-1099, octet 11 uses 1100-1199.
  vm_ids = [for i in range(local.node_count) : local.octet * 100 + i]

  # --- platform ------------------------------------------------------------
  # renovate: datasource=github-releases depName=siderolabs/talos
  talos_version = "v1.13.8"
  schematic_id  = "376567988ad370138ad8b2698212367b8edcb69b5fd68c80be1f2ec7d603b4ba"

  gitops_target_path = "clusters/management"

  # --- state database ------------------------------------------------------
  state_db_namespace = "database"
  state_db_cluster   = "tofu-state"
  state_db_name      = "tofu_state"
  state_db_owner     = "tofu"
  state_db_nodeport  = 30432
}

output "site_network" {
  description = "Everything derived from the site index."
  value = {
    site         = local.site_name
    site_cidr    = local.site_cidr
    node_cidr    = local.node_cidr
    node_gateway = local.node_gateway
    node_ips     = local.node_ips
    cluster_name = local.cluster_name
    vm_names     = local.vm_names
    hypervisors  = [for h in local.hypervisors : h.hostname]
    vm_placement = local.vm_placement
  }
}
