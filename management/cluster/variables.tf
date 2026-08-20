locals {
  config = jsondecode(file("../../config/management.rendered.json"))

  node_count    = 3
  talos_version = "v1.13.9"

  vlan_id   = 0
  base_cidr = "10.10.10.0/24"

  flux_target_path = "clusters/management"
}
