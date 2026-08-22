resource "talos_machine_secrets" "this" {
  talos_version = local.talos_version
}

data "talos_machine_configuration" "controlplane" {
  count              = local.node_count
  cluster_name       = local.cluster_name
  machine_type       = "controlplane"
  cluster_endpoint   = "https://${local.node_ips[0]}:6443"
  machine_secrets    = talos_machine_secrets.this.machine_secrets
  kubernetes_version = "1.31.1"

  config_patches = [
    yamlencode({
      machine = {
        install = {
          disk  = "/dev/vda"
          image = "factory.talos.dev/installer/${local.schematic_id}:${local.talos_version}"
        }
      }
      cluster = {
        allowSchedulingOnControlPlanes = true
      }
    })
  ]
}

resource "talos_machine_configuration_apply" "control_plane" {
  count                = local.node_count
  depends_on           = [proxmox_virtual_environment_vm.talos_cp]
  client_configuration = talos_machine_secrets.this.client_configuration

  machine_configuration_input = data.talos_machine_configuration.controlplane[count.index].machine_configuration
  node                        = local.node_ips[count.index]
}

resource "talos_machine_bootstrap" "this" {
  depends_on           = [talos_machine_configuration_apply.control_plane]
  client_configuration = talos_machine_secrets.this.client_configuration
  node                 = local.node_ips[0]
}

resource "talos_cluster_kubeconfig" "this" {
  depends_on           = [talos_machine_bootstrap.this]
  client_configuration = talos_machine_secrets.this.client_configuration
  node                 = local.node_ips[0]
}

data "talos_client_configuration" "this" {
  cluster_name         = local.cluster_name
  client_configuration = talos_machine_secrets.this.client_configuration
  endpoints            = [local.node_ips[0]]
}
