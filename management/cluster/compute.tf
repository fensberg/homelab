# =============================================================================
# Compute: the Talos control-plane virtual machines.
# Vendor: Proxmox VE (bpg/proxmox provider).
# =============================================================================

resource "proxmox_virtual_environment_file" "talos_iso" {
  content_type = "iso"
  datastore_id = "local-iso"
  node_name    = local.config.hypervisor.nodes[0].hostname

  source_file {
    path      = "https://factory.talos.dev/image/${local.schematic_id}/${local.talos_version}/nocloud-amd64.iso"
    file_name = "talos-${local.talos_version}-nocloud-amd64.iso"
  }
}

resource "proxmox_virtual_environment_vm" "talos_cp" {
  count      = local.node_count
  depends_on = [proxmox_virtual_environment_file.talos_iso]
  name       = "talos-cp-0${count.index + 1}"
  node_name  = local.config.hypervisor.nodes[0].hostname
  vm_id      = 100 + count.index

  # Disk first, ISO second. On first boot the disk is empty so it falls through
  # to the ISO; once Talos has installed itself it boots from disk.
  boot_order = ["virtio0", "ide0"]

  cpu {
    cores = 4
    type  = "x86-64-v2-AES"
  }

  memory {
    dedicated = 4096
  }

  network_device {
    bridge = "vnetint"
    model  = "virtio"
    # No vlan_id: the SDN zone is 'simple' and untagged. Setting it to 0
    # explicitly is not the same as leaving the port untagged.
  }

  disk {
    datastore_id = "local-zfs"
    file_format  = "raw"
    interface    = "virtio0"
    size         = 64
  }

  cdrom {
    file_id   = proxmox_virtual_environment_file.talos_iso.id
    interface = "ide0"
  }

  operating_system { type = "l26" }

  # Talos will not report ready to Proxmox without the guest-agent extension
  # baked into the Factory schematic. Leaving this off is deliberate.
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

    dns {
      servers = ["1.1.1.1", "1.0.0.1"]
    }

    # Static addressing derived from the index rather than listed per node, so
    # adding a node is a node_count bump and nothing else.
    ip_config {
      ipv4 {
        address = "${local.node_ips[count.index]}/24"
        gateway = local.node_gateway
      }
    }
  }
}
