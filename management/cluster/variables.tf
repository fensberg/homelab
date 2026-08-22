locals {
  config = jsondecode(file("../../config/management.rendered.json"))

  node_count = 3

  # renovate: datasource=github-releases depName=siderolabs/talos
  talos_version = "v1.13.8"
  schematic_id  = "376567988ad370138ad8b2698212367b8edcb69b5fd68c80be1f2ec7d603b4ba"

  base_cidr = "10.10.10.0/24"

  gitops_target_path = "clusters/management"

  # Namespace and cluster name for the OpenTofu state database.
  state_db_namespace = "database"
  state_db_cluster   = "tofu-state"
  state_db_name      = "tofu_state"
  state_db_owner     = "tofu"

  # NodePort the state database is reachable on from outside the cluster.
  # The workstation reaches it at <node ip>:<this> over the overlay network.
  state_db_nodeport = 30432
}
