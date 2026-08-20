data "talos_image_factory_urls" "this" {
  talos_version = local.talos_version
  platform      = "nocloud"
  endpoint      = "https://factory.talos.dev"
}

resource "proxmox_virtual_environment_download_file" "talos_iso" {
  content_type        = "iso"
  datastore_id        = "local-iso"
  node_name           = local.config.nodes[0].hostname
  url                 = "https://factory.talos.dev/image/${local.schematic_id}/${local.talos_version}/nocloud-amd64.iso"
  file_name           = "talos-nocloud-amd64.iso"
  overwrite           = true
  overwrite_unmanaged = true
}

resource "proxmox_virtual_environment_vm" "talos_cp" {
  count      = local.node_count
  depends_on = [proxmox_virtual_environment_download_file.talos_iso]
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
    enabled   = true
    file_id   = proxmox_virtual_environment_download_file.talos_iso.id
    interface = "ide0"
  }

  operating_system { type = "l26" }

  smbios {
    serial       = "talos-cp-0${count.index + 1}"
    manufacturer = "Sidero Labs"
    product      = "Talos Linux"
  }

  initialization {
    ip_config {
      ipv4 {
        address = "${cidrhost(local.base_cidr, 100 + count.index)}/24"
        gateway = cidrhost(local.base_cidr, 1)
      }
    }
  }
}
