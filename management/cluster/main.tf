resource "proxmox_virtual_environment_vm" "talos_cp" {
  count = local.node_count
  name  = "talos-cp-0${count.index + 1}"

  # Modulo Math: Distributes VMs based on the active length of the array
  node_name = local.config.nodes[count.index % length(local.config.nodes)].hostname

  vm_id = 100 + count.index

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
    file_id   = "local-iso:iso/talos-${local.talos_version}-metal-amd64.iso"
    interface = "ide0"
  }

  operating_system { type = "l26" }

  # SMBIOS Network Injection calculating via Array Modulo Math & Hardcoded CIDR
  args = [
    "-smbios",
    "type=11,value=ip=${cidrhost(local.base_cidr, 100 + count.index)}/24,gw=${cidrhost(local.base_cidr, 1)},dns=${cidrhost(local.base_cidr, 1)}"
  ]
}
