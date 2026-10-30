module "released" {
  # ok: a-module-is-fetched-as-released
  source = var.unreleased != "" ? "${var.unreleased}/parts/thing" : "oci://${var.release}//parts/thing?digest=${var.digest}"
}

module "another_when_checked" {
  # ruleid: a-module-is-fetched-as-released
  source = var.unreleased != "" ? "${var.unreleased}/parts/other" : "oci://${var.release}//parts/thing?digest=${var.digest}"
}

module "by_a_version_name" {
  # ruleid: a-module-is-fetched-as-released
  source = var.unreleased != "" ? "${var.unreleased}/parts/thing" : "oci://${var.release}//parts/thing?tag=v2026.10.1"
}

module "a_fixed_registry" {
  # ruleid: a-module-is-fetched-as-released
  source = var.unreleased != "" ? "${var.unreleased}/parts/thing" : "oci://registry.invalid/r//parts/thing?digest=${var.digest}"
}

module "a_fixed_digest" {
  # ruleid: a-module-is-fetched-as-released
  source = var.unreleased != "" ? "${var.unreleased}/parts/thing" : "oci://${var.release}//parts/thing?digest=sha256:abc"
}

module "by_path_when_released" {
  # ruleid: a-module-is-fetched-as-released
  source = var.unreleased != "" ? "${var.unreleased}/parts/thing" : "../../parts/thing"
}

module "beside_its_caller" {
  # ok: a-module-is-fetched-as-released
  source = "./parts"
}
