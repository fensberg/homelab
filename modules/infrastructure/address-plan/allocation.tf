# Every range a site owns, derived from its octet alone.
#
#   10.<o>.0.0/16            the site, carried over the overlay as one route
#     10.<o>.10.0/24         Talos nodes: control planes from .100, workers
#                            from .200
#     10.<o>.20.0/24         the load-balancer pool
#     10.<o>.30-39.0/24      untrusted zones, one /24 each
#   10.<100+o>.0.0/16        the site's pods
#   10.196.0.0/14, /22 #o    the site's services
#
# Pods and services are per site, site0 included. Every site used to share
# Kubernetes' defaults (10.244.0.0/16, 10.96.0.0/12), which is harmless until
# two sites route to each other and then impossible without a rebuild.
# Octets stop at 95 so the sites' /16s never reach the pod ranges above them.
locals {
  control_plane_band = 100
  worker_band        = 200
  dmz_first_subnet   = 30
  dmz_max_zones      = 10
  dmz_band           = 100

  ranges = {
    for key, s in var.sites : key => {
      octet        = s.octet
      site_cidr    = "10.${s.octet}.0.0/16"
      node_cidr    = "10.${s.octet}.10.0/24"
      node_gateway = cidrhost("10.${s.octet}.10.0/24", 1)
      lb_cidr      = "10.${s.octet}.20.0/24"
      pod_cidr     = "10.${100 + s.octet}.0.0/16"
      service_cidr = cidrsubnet("10.196.0.0/14", 8, s.octet)

      # The Talos templates each machine is cloned from, at the top of the
      # control-plane and untrusted bands' VM ids.
      template_vm_id     = s.octet * 1000 + 199
      dmz_template_vm_id = s.octet * 1000 + 399

      # The state database, as everything outside the cluster reaches it: the
      # first control plane, on the NodePort its Service is pinned to.
      state_database = {
        host = cidrhost("10.${s.octet}.10.0/24", local.control_plane_band)
        port = 30432
      }

      # The Proxmox SDN's identities for the site: its BGP autonomous system,
      # the two VXLAN network identifiers the playbook configures, and the name
      # of the vnet the nodes sit on. The playbook creates the vnet, so its
      # name is the estate's rather than a fact about any host; one name at
      # every site is safe because each site is its own Proxmox cluster.
      asn      = 65000 + s.octet
      vrf_vni  = 10000 + s.octet
      vnet_vni = 11000 + s.octet
      vnet     = "vnetint"
    }
  }
}
