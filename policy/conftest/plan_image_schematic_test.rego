package rules.plan_image_schematic_test

import data.rules.plan_image_schematic
import rego.v1

configured(resources) := {"path": "site-cluster.plan.json", "contents": {"configuration": {"root_module": {"module_calls": {"cluster": {"module": {"resources": resources}}}}}}}

image(name, refs) := {"address": sprintf("proxmox_download_file.%s", [name]), "mode": "managed", "type": "proxmox_download_file", "expressions": {"file_name": {"references": refs}}}

denies(i, words) if {
	some msg in plan_image_schematic.deny with input as i
	contains(msg, words)
}

test_a_path_of_version_and_schematic_passes if {
	count(plan_image_schematic.deny) == 0 with input as [configured([image("talos", ["local.talos_version", "local.schematic_id"]), image("dmz", ["local.talos_version", "local.dmz_schematic_id"])])]
}

test_a_path_without_the_schematic_is_refused if {
	denies([configured([image("talos", ["local.talos_version"])])], "module.cluster.proxmox_download_file.talos: the image path does not carry its schematic id")
}

test_a_path_without_the_version_is_refused if {
	denies([configured([image("talos", ["local.schematic_id"])])], "does not carry the Talos version")
}

test_a_constant_path_is_refused if {
	denies([configured([{"address": "proxmox_download_file.talos", "mode": "managed", "type": "proxmox_download_file", "expressions": {"file_name": {"constant_value": "talos.iso"}}}])], "does not carry its schematic id")
}

test_no_image_is_refused if {
	denies([configured([])], "checked nothing")
}
