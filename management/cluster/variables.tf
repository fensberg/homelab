variable "site_index" {
  type        = number
  default     = 0
  description = <<-EOT
    Which entry in the config's sites[] array to deploy. The index IS the
    site's identity: it names the site, picks its network, and numbers its
    VMs. Set by the start button via TF_VAR_site_index.
  EOT
}

locals {
  config = jsondecode(file("../../config/management.rendered.json"))

  site      = local.config.sites[var.site_index]
  site_name = "site${var.site_index}"

  hypervisors = local.site.hypervisor.nodes
  node_count  = local.site.control_plane_count

  # --- addressing ----------------------------------------------------------
  # The octet is derived from the array index, not configured. Two sites
  # cannot share one, because two array entries cannot share an index - so
  # the collision that would present as a broken overlay network is not
  # expressible rather than merely tested for.
  #
  # Site 0 is 10.10.0.0/16, site 1 is 10.11.0.0/16, and so on.
  octet     = 10 + var.site_index
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
  cluster_name = "${local.config.organization.name}-${local.site_name}"
  vm_names     = [for i in range(local.node_count) : format("%s-cp-%02d", local.site_name, i + 1)]

  # VM IDs are banded by site so two sites could share a Proxmox cluster
  # without colliding: site 0 uses 100-199, site 1 uses 200-299.
  vm_ids = [for i in range(local.node_count) : (var.site_index + 1) * 100 + i]

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
