variable "site" {
  type        = string
  default     = "site0"
  description = <<-EOT
    Which key in the config's sites map to deploy, e.g. "site0". Selection
    only - the site's identity comes from the octet it declares. Set by the
    start button via TF_VAR_site.
  EOT
}

locals {
  config = jsondecode(file("../../config/management.rendered.json"))

  site = local.config.sites[var.site]

  # Everything nameable uses the site's own name, so Proxmox, Talos, kubeconfig
  # and the Tailscale console all say "sheridan" rather than a positional key
  # nobody recognises. The name is a vault reference, so it never reaches git.
  #
  # Sanitised because these become Proxmox VM names and a Talos cluster name,
  # which are DNS-shaped: a label like "Sheridan Road Office" has to collapse
  # to "sheridan-road-office". Falls back to the map key if the name is blank.
  site_name = (
    trim(lower(replace(try(local.site.name, ""), "/[^A-Za-z0-9]+/", "-")), "-") != ""
    ? trim(lower(replace(local.site.name, "/[^A-Za-z0-9]+/", "-")), "-")
    : var.site
  )

  # nodes is a map, so iterate it in sorted key order: node0, node1, node2.
  # HCL orders map iteration lexicographically, which keeps VM placement
  # deterministic between runs and matches what the start button does.
  hypervisors = [for k in sort(keys(local.site.hypervisor.nodes)) : local.site.hypervisor.nodes[k]]

  # Per-site because two sites are two estates: separate hypervisors, separate
  # tailnets when the engagement calls for it, separate buckets, and separate
  # state databases. Sharing any of them means compromising one site reaches
  # the others.
  overlay_network = local.site.overlay_network
  object_storage  = local.site.object_storage
  site_state      = local.site.state
  node_count      = local.site.control_plane_count

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
  cluster_name = trim(lower(replace(
    "${local.config.organization.name}-${local.site_name}",
  "/[^A-Za-z0-9]+/", "-")), "-")
  vm_names = [for i in range(local.node_count) : format("%s-cp-%02d", local.site_name, i + 1)]

  # Banded by octet so two sites could share a Proxmox cluster without
  # colliding: octet 10 uses 1000-1099, octet 11 uses 1100-1199.
  vm_ids = [for i in range(local.node_count) : local.octet * 100 + i]

  # --- platform ------------------------------------------------------------
  # renovate: datasource=github-releases depName=siderolabs/talos
  talos_version = "v1.13.8"
  # Two system extensions, generated via factory.talos.dev's schematic API -
  # both are Longhorn's own documented prerequisites for Talos Linux:
  # siderolabs/iscsi-tools (iscsid/iscsiadm, which Kubernetes persistent
  # volume operations need) and siderolabs/util-linux-tools (fstrim, for
  # volume trimming). Talos ships neither by default.
  schematic_id = "613e1592b2da41ae5e265e8789429f22e121aab91cb4deb6bc3c0b6262961245"

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
