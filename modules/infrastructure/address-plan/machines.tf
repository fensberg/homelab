# Every machine a site runs: its address, VM id, name and hypervisor.
#
# Reproduced exactly from what the cluster root computed before this module
# existed, because a machine whose address or VM id moves is a machine the
# next plan replaces.
locals {
  # A site's name, sanitised into something DNS-shaped: VM names, the Talos
  # cluster name and every derived name use it. Falls back to the key.
  slugs = {
    for key, s in var.sites : key => (
      trim(lower(replace(try(s.name, ""), "/[^A-Za-z0-9]+/", "-")), "-") != ""
      ? trim(lower(replace(s.name, "/[^A-Za-z0-9]+/", "-")), "-")
      : key
    )
  }

  hypervisors = {
    for key, s in var.sites : key => [
      for n in sort(keys(try(s.hypervisor.nodes, {}))) : s.hypervisor.nodes[n]
    ]
  }

  control_planes = {
    for key, s in var.sites : key => {
      for i in range(s.control_plane_count) : tostring(local.control_plane_band + i) => {
        host_octet = local.control_plane_band + i
        ip         = cidrhost(local.ranges[key].node_cidr, local.control_plane_band + i)
        name       = format("%s-cp-%d", local.slugs[key], local.control_plane_band + i)
        vm_id      = s.octet * 1000 + local.control_plane_band + i
        hypervisor = length(local.hypervisors[key]) > 0 ? local.hypervisors[key][i % length(local.hypervisors[key])].hostname : ""
      }
    }
  }

  workers = {
    for key, s in var.sites : key => {
      for i in range(try(s.worker_count, 0)) : tostring(local.worker_band + i) => {
        host_octet = local.worker_band + i
        ip         = cidrhost(local.ranges[key].node_cidr, local.worker_band + i)
        name       = format("%s-wk-%d", local.slugs[key], local.worker_band + i)
        vm_id      = s.octet * 1000 + local.worker_band + i
        hypervisor = length(local.hypervisors[key]) > 0 ? local.hypervisors[key][i % length(local.hypervisors[key])].hostname : ""
      }
    }
  }

  dmz_zones = {
    for key, s in var.sites : key => {
      for i, zone in sort(keys(try(s.dmz_zones, {}))) : zone => {
        name    = zone
        index   = i
        cidr    = "10.${s.octet}.${local.dmz_first_subnet + i}.0/24"
        gateway = "10.${s.octet}.${local.dmz_first_subnet + i}.1"
        vnet    = "vnetdmz${i}"
        vni     = 12000 + s.octet * 100 + i
        nodes   = try(s.dmz_zones[zone].node_count, 0) > 0 ? s.dmz_zones[zone].node_count : 1
      }
    }
  }

  dmz = {
    for key, s in var.sites : key => merge({}, [
      for zone, z in local.dmz_zones[key] : {
        for j in range(z.nodes) : "${zone}-${local.dmz_band + j}" => {
          zone       = zone
          host_octet = local.dmz_band + j
          ip         = cidrhost(z.cidr, local.dmz_band + j)
          name       = "${local.slugs[key]}-${zone}-${local.dmz_band + j}"
          vm_id      = s.octet * 1000 + 300 + z.index * 10 + j
          hypervisor = length(local.hypervisors[key]) > 0 ? local.hypervisors[key][(z.index + j) % length(local.hypervisors[key])].hostname : ""
        }
      }
    ]...)
  }
}
