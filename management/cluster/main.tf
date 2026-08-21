resource "proxmox_virtual_environment_file" "talos_iso" {
  content_type = "iso"
  datastore_id = "local-iso"
  node_name    = local.config.nodes[0].hostname

  source_file {
    path      = "https://factory.talos.dev/image/376567988ad370138ad8b2698212367b8edcb69b5fd68c80be1f2ec7d603b4ba/${local.talos_version}/nocloud-amd64.iso"
    file_name = "talos-${local.talos_version}-nocloud-amd64.iso"
  }
}

resource "proxmox_virtual_environment_vm" "talos_cp" {
  count      = local.node_count
  depends_on = [proxmox_virtual_environment_file.talos_iso]
  name       = "talos-cp-0${count.index + 1}"
  node_name  = local.config.nodes[0].hostname
  vm_id      = 100 + count.index

  cpu {
    cores = 4
    type  = "x86-64-v2-AES"
  }

  memory {
    dedicated = 4096
  }

  network_device {
    bridge  = "vnetint"
    vlan_id = local.vlan_id
  }

  disk {
    datastore_id = "local-zfs"
    file_format  = "raw"
    interface    = "virtio0"
    size         = 32
  }

  cdrom {
    file_id   = proxmox_virtual_environment_file.talos_iso.id
    interface = "ide0"
  }

  operating_system { type = "l26" }

  agent {
    enabled = false
  }

  smbios {
    serial       = "talos-cp-0${count.index + 1}"
    manufacturer = "Sidero Labs"
    product      = "Talos Linux"
  }

  initialization {
    datastore_id = "local-zfs"
    ip_config {
      ipv4 {
        address = "${cidrhost(local.base_cidr, 100 + count.index)}/24"
        gateway = cidrhost(local.base_cidr, 1)
      }
    }
  }
}
