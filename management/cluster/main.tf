locals {
  config = jsondecode(file("../../config/management.rendered.json"))
}

resource "talos_machine_secrets" "this" {
  talos_version = "v1.7.0"
}

resource "proxmox_virtual_environment_vm" "talos_node" {
  count     = local.config.nodes.control_plane
  name      = "talos-cp-0${count.index + 1}"
  node_name = local.config.infrastructure.target_hypervisor

  agent { enabled = true }

  cpu {
    cores = 4
    type  = "x86-64-v2-AES"
  }
  memory { dedicated = 4096 }

  network_device {
    bridge = "vnetint"
  }

  disk {
    datastore_id = "local-zfs"
    file_format  = "raw"
    interface    = "virtio0"
    size         = 32
  }

  operating_system { type = "l26" }

  initialization {
    datastore_id = "local-zfs"
    ip_config {
      ipv4 {
        address = "${cidrhost(local.config.network.base_cidr, count.index + 11)}/24"
        gateway = cidrhost(local.config.network.base_cidr, 1)
      }
    }
  }
}

resource "talos_machine_configuration_apply" "control_plane" {
  count                       = local.config.nodes.control_plane
  client_configuration        = talos_machine_secrets.this.client_configuration
  machine_configuration_input = talos_machine_secrets.this.machine_configuration
  node                        = cidrhost(local.config.network.base_cidr, count.index + 11)
  depends_on                  = [proxmox_virtual_environment_vm.talos_node]
}

resource "talos_machine_bootstrap" "this" {
  client_configuration = talos_machine_secrets.this.client_configuration
  node                 = cidrhost(local.config.network.base_cidr, 11)
  depends_on           = [talos_machine_configuration_apply.control_plane]
}

resource "talos_cluster_kubeconfig" "this" {
  client_configuration = talos_machine_secrets.this.client_configuration
  node                 = cidrhost(local.config.network.base_cidr, 11)
  depends_on           = [talos_machine_bootstrap.this]
}
