resource "talos_machine_secrets" "this" {
  talos_version = local.talos_version
}

data "talos_machine_configuration" "controlplane" {
  cluster_name     = local.config.organization.name
  machine_type     = "controlplane"
  cluster_endpoint = "https://${cidrhost(local.base_cidr, 100)}:6443"
  machine_secrets  = talos_machine_secrets.this.machine_secrets

  kubernetes_version = "1.31.1"

  config_patches = [
    yamlencode({
      machine = {
        install = {
          disk = "/dev/vda"
        }
      },
      cluster = {
        allowSchedulingOnControlPlanes = true
      }
    })
  ]
}

resource "talos_machine_configuration_apply" "control_plane" {
  count                       = local.node_count
  depends_on                  = [proxmox_virtual_environment_vm.talos_cp]
  client_configuration        = talos_machine_secrets.this.client_configuration
  machine_configuration_input = data.talos_machine_configuration.controlplane.machine_configuration

  node = cidrhost(local.base_cidr, 100 + count.index)
}

resource "talos_machine_bootstrap" "this" {
  depends_on           = [talos_machine_configuration_apply.control_plane]
  client_configuration = talos_machine_secrets.this.client_configuration
  node                 = cidrhost(local.base_cidr, 100)
}

resource "talos_cluster_kubeconfig" "this" {
  depends_on           = [talos_machine_bootstrap.this]
  client_configuration = talos_machine_secrets.this.client_configuration
  node                 = cidrhost(local.base_cidr, 100)
}

data "talos_client_configuration" "this" {
  cluster_name         = local.config.organization.name
  client_configuration = talos_machine_secrets.this.client_configuration
  endpoints            = [cidrhost(local.base_cidr, 100)]
}
