# An image must come from an approved registry and be pinned by digest.
#
# The digest half matters as much as the registry. A tag is a mutable pointer:
# the same name resolves to different software over time and git records
# nothing when it moves, so a tagged image from an approved registry is still
# an unreviewed change waiting to happen.
package rules.supplier_images

import data.lib
import rego.v1

list := [f.contents | some f in lib.at("scripts/approved-suppliers.yml")][0]

dir(path) := concat("/", array.slice(parts, 0, count(parts) - 1)) if parts := split(path, "/")

flux contains f if {
	some f in lib.under("clusters/")
	endswith(f.path, ".yaml")
}

ref_shape := `^[a-zA-Z0-9][a-zA-Z0-9./:@_-]+$`

# Every image a manifest names, as a field or as a line of a value that
# carries YAML of its own.
images contains [f.path, ref] if {
	some f in flux
	walk(f.contents, [path, ref])
	count(path) > 0
	path[count(path) - 1] == "image"
	is_string(ref)
	regex.match(ref_shape, ref)
}

images contains [f.path, m[1]] if {
	some f in flux
	walk(f.contents, [_, v])
	is_string(v)
	contains(v, "\n")
	some m in regex.find_all_string_submatch_n(`(?m)^\s*(?:-\s*)?image:\s*["']?([a-zA-Z0-9][a-zA-Z0-9./:@_-]+)["']?\s*$`, v, -1)
}

remote contains [path, ref] if {
	some [path, ref] in images
	contains(ref, "/")
}

overlay_pins(at, name) if {
	some k in lib.named("kustomization.yaml")
	k.path == $"{at}/kustomization.yaml"
	some image in object.get(k.contents, "images", [])
	image.name == name
	startswith(object.get(image, "digest", ""), "sha256:")
}

approved_registries contains r.host if {
	some r in object.get(list, "registries", [])
}

exempt contains e.path if {
	some e in object.get(list, "exemptions", [])
	e.rule == "digest-pinned-images"
}

unpinned contains [path, ref] if {
	some [path, ref] in remote
	not contains(ref, "@sha256:")
	name := split(ref, ":")[0]
	not overlay_pins(dir(path), name)
}

deny contains msg if {
	count(remote) == 0
	msg := "no image references found under clusters/, so this rule proves nothing"
}

deny contains msg if {
	some [path, ref] in remote
	host := split(ref, "/")[0]
	not host in approved_registries
	msg := $"{path}: image \"{ref}\" comes from \"{host}\", which is not an approved supplier. Add it to scripts/approved-suppliers.yml with a reason, or use a registry already approved."
}

deny contains msg if {
	some [path, ref] in unpinned
	not path in exempt
	msg := $"{path}: image \"{ref}\" is not pinned by digest. A tag is a mutable pointer - the same name is different software later, and git records nothing when it moves. If this genuinely cannot be pinned, declare it under exemptions: in scripts/approved-suppliers.yml with a reason and a trigger."
}

deny contains msg if {
	some path in exempt
	not path in {p | some [p, _] in unpinned}
	msg := $"{path} is exempted from digest pinning but no longer needs to be. Remove the exemption from scripts/approved-suppliers.yml - a stale excuse reads as a live gap."
}
