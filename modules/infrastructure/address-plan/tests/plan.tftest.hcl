# The address plan, asserted on its own: what one site gets, that two sites
# never share a range, what it refuses, and when it names things.

variables {
  sites = {
    site0 = {
      name                = "North Street Office"
      octet               = 10
      control_plane_count = 3
      worker_count        = 2
      dmz_zones           = { games = { node_count = 2 } }
      hypervisor = { nodes = {
        node0 = { hostname = "hv-a", ip = "192.0.2.10" }
        node1 = { hostname = "hv-b", ip = "192.0.2.11" }
      } }
    }
    site1 = {
      name                = ""
      octet               = 20
      control_plane_count = 1
      hypervisor          = { nodes = { node0 = { hostname = "hv-c", ip = "198.51.100.10" } } }
    }
  }
}

# The addresses machines already have. A change to any of these is a machine
# the next converge replaces.
run "a_site_keeps_its_machines_addresses" {
  command = plan

  assert {
    condition     = output.sites.site0.control_planes["100"].ip == "10.10.10.100" && output.sites.site0.control_planes["100"].vm_id == 10100
    error_message = "the first control plane moved"
  }
  assert {
    condition     = output.sites.site0.workers["201"].ip == "10.10.10.201" && output.sites.site0.workers["201"].vm_id == 10201
    error_message = "a worker moved"
  }
  assert {
    condition     = output.sites.site0.dmz["games-101"].ip == "10.10.30.101" && output.sites.site0.dmz["games-101"].vm_id == 10301
    error_message = "an untrusted machine moved"
  }
  assert {
    condition     = output.sites.site0.control_planes["101"].hypervisor == "node1"
    error_message = "placement is no longer round-robin over the sorted hypervisors, or a machine names its hypervisor by something other than its config key"
  }
  assert {
    condition     = output.sites.site0.slug == "north-street-office" && output.sites.site1.slug == "site1"
    error_message = "a slug is not the sanitised name, falling back to the key"
  }
}

# Pods and services are the site's own, site0 included, and no two sites
# share any range at all.
run "no_two_sites_share_a_range" {
  command = plan

  assert {
    condition     = output.sites.site0.pod_cidr == "10.110.0.0/16" && output.sites.site0.service_cidr == "10.196.40.0/22"
    error_message = "site0's pods and services are not its own"
  }
  assert {
    condition = length(distinct(flatten([
      for s in values(output.sites) : [s.site_cidr, s.pod_cidr, s.service_cidr]
    ]))) == 3 * length(output.sites)
    error_message = "two sites share a range"
  }
  assert {
    condition     = output.sites.site1.asn == 65020 && output.sites.site1.vnet_vni == 11020
    error_message = "a site's SDN identities are not derived from its octet"
  }
  # Proxmox caps a vnet id at eight characters, and the nodes' vnet must not
  # be an untrusted zone's: that is the whole of what a zone's own vnet buys.
  assert {
    condition = alltrue([
      for s in values(output.sites) :
      length(s.vnet) > 0 && length(s.vnet) <= 8 && !contains([for z in values(s.dmz_zones) : z.vnet], s.vnet)
    ])
    error_message = "a site's nodes do not sit on a vnet of their own that Proxmox accepts"
  }
}

# A route is declared once and lands in each site's own service range, at
# the same host number, inside the band Kubernetes never allocates itself.
run "a_fixed_address_is_each_sites_own" {
  command = plan

  variables {
    fixed_addresses = { game-server = 46 }
  }

  assert {
    condition     = output.sites.site0.fixed_addresses["game-server"] == "10.196.40.46" && output.sites.site1.fixed_addresses["game-server"] == "10.196.80.46"
    error_message = "a fixed address is not the host number in each site's own service range"
  }
}

run "a_fixed_address_outside_the_hand_picked_band_is_refused" {
  command = plan

  variables {
    fixed_addresses = { game-server = 64 }
  }

  expect_failures = [var.fixed_addresses]
}

run "two_fixed_addresses_cannot_share_a_host" {
  command = plan

  variables {
    fixed_addresses = { game-server = 46, dashboard = 46 }
  }

  expect_failures = [var.fixed_addresses]
}

run "names_wait_for_a_domain" {
  command = plan

  assert {
    condition     = length(output.records) == 0
    error_message = "names were invented with no domain declared"
  }
}

run "names_and_aliases_once_there_is_a_domain" {
  command = plan

  variables {
    domain  = "example.com"
    aliases = { proxmox = "site0" }
  }

  assert {
    condition     = output.records["cp-100.site0.example.com"] == "10.10.10.100"
    error_message = "a machine has no name"
  }
  assert {
    condition     = output.records["proxmox.site0.example.com"] == "192.0.2.10"
    error_message = "a site's hypervisor has no name"
  }
  assert {
    condition     = output.records["proxmox.example.com"] == output.records["proxmox.site0.example.com"]
    error_message = "the alias does not point where it says"
  }
}

run "an_alias_to_an_undeclared_site_is_refused" {
  command = plan

  variables {
    domain  = "example.com"
    aliases = { proxmox = "site9" }
  }
  expect_failures = [var.aliases]
}

# The control-plane and worker bands cannot meet. talos.tf merges the two maps
# keyed by host octet, and a duplicate key there drops a machine silently
# rather than failing. A hundred control planes is the most the band holds.
run "the_bands_cannot_collide" {
  command = plan

  variables {
    sites = {
      a = { octet = 10, control_plane_count = 100, worker_count = 1 }
    }
  }

  assert {
    condition = length(distinct(concat(
      [for m in values(output.sites.a.control_planes) : m.ip],
      [for m in values(output.sites.a.workers) : m.ip],
    ))) == 101
    error_message = "a control plane and a worker share an address; the bands overlap"
  }
}

# An alias to something the site does not have is reported for the edge to
# refuse, rather than failing the plan here or vanishing without a word.
run "an_alias_to_a_missing_service_is_reported" {
  command = plan

  variables {
    domain  = "example.invalid"
    aliases = { grafana = "site0" }
  }

  assert {
    condition     = length(output.unresolved_aliases) == 1 && output.unresolved_aliases[0] == "grafana -> site0" && !contains(keys(output.records), "grafana.example.invalid")
    error_message = "an alias with no target was not reported, or was given a record"
  }
}
