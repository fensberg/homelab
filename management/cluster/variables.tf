locals {
  config = jsondecode(file("../../config/management.rendered.json"))

  node_count    = 3
  talos_version = "v1.13.9"
  schematic_id = 376567988ad370138ad8b2698212367b8edcb69b5fd68c80be1f2ec7d603b4ba

  vlan_id   = 0
  base_cidr = "10.10.10.0/24"

  flux_target_path = "clusters/management"
}
