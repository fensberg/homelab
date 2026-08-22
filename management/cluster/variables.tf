variable "site" {
  type        = string
  description = <<-EOT
    Which site in the fleet this deployment is for, e.g. "chicago". Set by the
    start button via TF_VAR_site; running tofu by hand needs it too.
  EOT
}

locals {
  config = jsondecode(file("../../config/management.rendered.json"))

  # The fleet arrives as a JSON string because a 1Password field is a string.
  # Topology lives there rather than in git: for an MSP, client names, network
  # ranges and hypervisor addresses are reconnaissance material.
  #
  # Indexing directly means an unknown site fails at plan time rather than
  # producing a half-configured cluster. The start button checks first and
  # gives a friendlier message.
  fleet = jsondecode(local.config.fleet)
  this  = local.fleet[var.site]

  hypervisors = local.this.hypervisor.nodes
  node_count  = local.this.control_plane_count

  # --- addressing ----------------------------------------------------------
  # One /16 per site, advertised as a single route over the overlay network, so
  # a site can grow new subnets without anyone touching the tailnet policy.
  site_cidr = "10.${local.this.octet}.0.0/16"

  # Talos control-plane nodes. Hypervisor infrastructure sits in .0.0/24 and a
  # load-balancer pool is reserved at .20.0/24 for epoch 02.
  node_cidr    = "10.${local.this.octet}.10.0/24"
  node_gateway = cidrhost(local.node_cidr, 1)
  node_ips     = [for i in range(local.node_count) : cidrhost(local.node_cidr, 100 + i)]

  # DHCP pool for the SDN subnet, disjoint from the static nodes above.
  dhcp_start = cidrhost(local.node_cidr, 50)
  dhcp_end   = cidrhost(local.node_cidr, 99)

  # --- placement -----------------------------------------------------------
  # Control-plane VMs are dealt round-robin across whatever hypervisors the
  # site has. One hypervisor puts all three on it; three put one on each, which
  # is what makes the cluster survive losing a box. Appending a node to the
  # fleet is all it takes - but see the SDN note in docs/epochs/01-ignition.md,
  # because a multi-node Proxmox cluster also needs a vxlan or evpn zone.
  vm_placement = [
    for i in range(local.node_count) :
    local.hypervisors[i % length(local.hypervisors)].hostname
  ]

  # --- identity ------------------------------------------------------------
  cluster_name = "${local.config.organization.name}-${var.site}"

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
  description = "Addressing and placement for this deployment, derived from the fleet."
  value = {
    site         = var.site
    site_cidr    = local.site_cidr
    node_cidr    = local.node_cidr
    node_gateway = local.node_gateway
    node_ips     = local.node_ips
    cluster_name = local.cluster_name
    hypervisors  = [for h in local.hypervisors : h.hostname]
    vm_placement = local.vm_placement
  }
}
