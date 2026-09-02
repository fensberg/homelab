# =============================================================================
# Compute: the Talos control-plane virtual machines.
# Vendor: Proxmox VE (bpg/proxmox provider).
# =============================================================================

# One copy per hypervisor that will host a VM. A Proxmox node can only import a
# disk image into its own datastore, so this scales with the node list rather
# than assuming a single box.
#
# A pre-installed disk image, not an installer ISO. The ISO-then-install
# approach - boot from ISO into maintenance mode, apply a config that
# includes machine.install, let that apply trigger the install - is what
# every earlier version of this file used, and Talos's own install process
# reliably corrupted the node's network config the moment it ran (see the
# epoch record's "Blocked" section for the full diagnosis). A disk image
# sidesteps the failure mode entirely: Talos is already installed the moment
# the VM boots, so there is no install-time transition left to corrupt
# anything. talos.tf's config_patches carry no `machine.install` section any
# more - there is nothing left for it to do.
#
# Image Factory serves this compressed (`.raw.xz`); the provider's own docs
# are explicit that compressed images cannot use `import_from` - they need
# `file_id` with `content_type = "iso"`, and Proxmox's zstd decompressor
# transparently handles the xz stream despite the mismatched name.
# Orphan adoption (if this file already exists outside Terraform - a prior
# run's incomplete teardown) happens in Go, in compute.go, before this gets
# applied - not with a static `import` block here. import blocks always
# attempt the read and hard-fail if the target genuinely does not exist
# ("failed reading ..."), which is the normal, common case for this
# resource; there is no declarative way to make the attempt conditional on
# the object actually being there first. Confirmed the hard way: an import
# block here broke a completely ordinary fresh-create run the same day it
# was added.
resource "proxmox_download_file" "talos_disk_image" {
  for_each = toset(local.vm_placement)

  content_type = "iso"
  datastore_id = "local-iso"
  node_name    = each.value
  url          = "https://factory.talos.dev/image/${local.schematic_id}/${local.talos_version}/nocloud-amd64.raw.xz"
  # Extension is .iso, not .img, on purpose: local-iso's content=iso bucket
  # validates the destination file_name against that content type before
  # Proxmox even fetches the URL, independent of the actual bytes. This is
  # the same disk image either way - the extension just has to lie to get
  # stored where compressed non-ISO images are allowed to live.
  file_name               = "talos-${local.talos_version}-nocloud-amd64.iso"
  decompression_algorithm = "zst"

  # Without this, the provider compares the URL's advertised size (the
  # compressed .raw.xz, ~200MB) against the size actually stored in the
  # datastore (the decompressed raw disk, ~4.5GB), sees a mismatch every
  # single plan, and forces a destroy-and-reimport - even on a plan that
  # otherwise has nothing to do with this resource. size is provider-computed
  # with nothing configured to compare against, so lifecycle.ignore_changes
  # cannot suppress this; overwrite=false is the mechanism the provider's own
  # plan output names for exactly this case.
  overwrite = false
}

# One template VM per hypervisor, built once from the downloaded disk image.
# This is the only place file_id-based disk creation happens - it requires
# Terraform to SSH into the node and run pvesm/qm commands directly (see
# versions.tf's ssh block), which bpg/proxmox's own docs frame as an
# edge-case operation, not the default path. An earlier version of this file
# ran that same SSH-based import for every control-plane node on every
# deploy, which turned out to be genuinely unreliable in this environment
# (a pmxcfs "ipcc_send_rec" IPC error, reproduced even with a single,
# fully-isolated import - not a concurrency problem, the mechanism itself).
# Importing once into a template and cloning for every real node is the
# standard pattern every other Talos-on-Proxmox Terraform example uses:
# cloning is a native, API-only Proxmox operation with no SSH involved at
# all, and the provider documents built-in retries for concurrent clones -
# a resilience feature the file_id path simply has none of.
resource "proxmox_virtual_environment_vm" "talos_template" {
  for_each = toset(local.vm_placement)

  name      = "${local.site_name}-talos-template"
  node_name = each.value
  # One offset per octet band, above every real control-plane ID that band
  # can ever hold: vm_ids end at octet*1000 + 100 + N-1, and N is well
  # under 99, so the top of the band is free.
  vm_id = local.octet * 1000 + 199

  template = true
  started  = false

  boot_order = ["virtio0"]

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
  }

  disk {
    datastore_id = "local-zfs"
    file_format  = "raw"
    interface    = "virtio0"
    file_id      = proxmox_download_file.talos_disk_image[each.value].id
  }

  operating_system { type = "l26" }

  agent {
    enabled = false
  }

  # Stop, do not ask.
  #
  # The provider's default is a graceful ACPI shutdown with
  # timeout_shutdown_vm at 1800 seconds, and its own documentation says that
  # when the QEMU agent is disabled "the VM may not be able to shutdown
  # properly, and may need to be forced off", recommending this setting. The
  # agent is disabled here deliberately - Talos will not report ready without
  # the extension in the Factory schematic - so this estate is precisely the
  # configuration that recommendation is written for.
  #
  # Found by a real teardown: five VMs sat in "still destroying" while the
  # provider waited politely for machines that were never going to answer.
  # Thirty minutes each is the documented worst case.
  #
  # The verb decides the manners. A converge is surgical and would want a
  # clean shutdown; a demolish is demolition, and the machines it is stopping
  # are ones it is about to delete. There is nothing on them to lose - that is
  # what makes the whole operation acceptable in the first place.
  stop_on_destroy = true

  smbios {
    serial       = "${local.site_name}-talos-template"
    manufacturer = "Sidero Labs"
    product      = "Talos Linux"
  }

  lifecycle {
    # file_id is a stable string ("local-iso:iso/talos-<version>-....iso") -
    # the datastore path doesn't change even when the schematic (hence the
    # actual image bytes at that path) does, since neither is part of the
    # file name. Without this, replacing the disk image resource silently
    # leaves this template's already-materialized OS disk on the old bytes:
    # confirmed by a real apply that changed the schematic and still showed
    # "0 to change" here. replace_triggered_by forces the rebuild on any
    # change to the upstream resource, independent of whether file_id's own
    # value looks different. [each.key], not [each.value]: OpenTofu only
    # permits each.key in this specific expression (confirmed by a real
    # validate error - each.value, identical in value for this set-keyed
    # for_each, is still rejected syntactically) - and not the bare
    # resource name either, which would tie every template to every
    # hypervisor's image instead of just its own.
    replace_triggered_by = [proxmox_download_file.talos_disk_image[each.key]]
  }
}

resource "proxmox_virtual_environment_vm" "talos_cp" {
  # Keyed by host octet, so a node's identity is the machine rather than its
  # position - see the control_plane local in variables.tf.
  for_each  = local.control_plane
  name      = each.value.name
  node_name = each.value.hypervisor
  vm_id     = each.value.vm_id

  boot_order = ["virtio0"]

  clone {
    vm_id = proxmox_virtual_environment_vm.talos_template[each.value.hypervisor].vm_id
    full  = true
  }

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
  }

  disk {
    datastore_id = "local-zfs"
    file_format  = "raw"
    interface    = "virtio0"
    # Grown from the image's native size to the size a real control-plane
    # node needs. Talos grows its state/ephemeral partitions to fill
    # whatever the disk actually offers, so this alone is enough. Every
    # other non-default attribute (datastore_id, file_format) has to be
    # re-specified here too, or the schema defaults silently override the
    # cloned source's values - bpg/proxmox's own documented behavior for
    # modifying a disk inherited from a clone.
    size = 64
  }

  # The storage provisioner's data path, kept off the OS disk on purpose:
  # Talos grows disk 0 to fill with its own state and ephemeral partitions,
  # so a provisioner sharing it has no predictable capacity. talos.tf mounts
  # this one at /var/mnt/storage, which is where OpenEBS hands out volumes.
  disk {
    datastore_id = "local-zfs"
    file_format  = "raw"
    interface    = "virtio1"
    size         = 32
  }

  operating_system { type = "l26" }

  agent {
    enabled = false
  }

  smbios {
    serial       = each.value.name
    manufacturer = "Sidero Labs"
    product      = "Talos Linux"
  }

  initialization {
    datastore_id = "local-zfs"

    dns {
      servers = ["1.1.1.1", "1.0.0.1"]
    }

    ip_config {
      ipv4 {
        address = "${each.value.ip}/24"
        gateway = local.node_gateway
      }
    }
  }
}
