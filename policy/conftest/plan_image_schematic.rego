# A Talos image's datastore path changes when its schematic does.
#
# WHAT WAS WRONG. proxmox_download_file is identified by its datastore path,
# and the path was talos-<version>.iso. A schematic id says WHICH extensions
# are baked into the image, so re-minting one - the only way to add, remove
# or move an extension - produced no plan diff at all: same name, same
# resource, nothing to do (#97). The new image was never fetched, and the
# replace_triggered_by on the template could not fire, because the thing it
# triggers on had not changed. A schematic bump then looks applied - clean
# plan, successful apply - while every node keeps the old extensions. That is
# how a known-vulnerable Tailscale reached every machine in the estate and
# stayed there.
#
# ON THE PLAN'S CONFIGURATION: what each image's path is made from. The
# version resolves every extension's build too, so two images from one
# schematic on different Talos releases are different bytes at one path.
#
# This asserts the identity only. Whether a rebuilt template reaches
# machines already running is node lifecycle, which epoch 05 owns.
package rules.plan_image_schematic

import data.lib
import rego.v1

images contains [address, r] if {
	some [_, address, r] in lib.plan_configuration
	r.type == "proxmox_download_file"
}

carries(r, part) if {
	some ref in object.get(object.get(r.expressions, "file_name", {}), "references", [])
	parts := split(ref, ".")
	contains(parts[count(parts) - 1], part)
}

deny contains msg if {
	count(images) == 0
	msg := "no proxmox_download_file is in any plan's configuration, so this checked nothing"
}

deny contains msg if {
	some [address, r] in images
	not carries(r, "schematic_id")
	msg := sprintf("%s: the image path does not carry its schematic id. The resource is identified by that path, so re-minting a schematic is not a change: the image is never re-fetched, and the replace_triggered_by on the template has nothing to fire on.", [address])
}

deny contains msg if {
	some [address, r] in images
	not carries(r, "talos_version")
	msg := sprintf("%s: the image path does not carry the Talos version. The version resolves every extension's build, so two images from one schematic on different Talos releases are different bytes at the same path.", [address])
}
