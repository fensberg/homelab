# What the site's machines should be running, put where the play that rolls
# them reads it (management/roll).
#
# The site runs the platform release its line pins, so what its machines
# should run is that release's, not whatever main's modules say today. This
# is the release saying it, in the cluster: the play compares it with what
# each machine reports running, and rolls the ones that differ, one at a time.
# OpenTofu declares; the play orders; nothing here changes a machine.

module "machine_image" {
  source = "../machine-image"
}

resource "kubernetes_config_map" "machine_image" {
  metadata {
    name      = "machine-image"
    namespace = "kube-system"
  }

  data = {
    talos_version       = module.machine_image.talos_version
    talos_upgrades_from = module.machine_image.talos_upgrades_from
    kubernetes_version  = module.machine_image.kubernetes_version
    installer_image     = module.machine_image.installer_image
  }
}
