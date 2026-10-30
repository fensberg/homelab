# Charts decide what images get deployed, so an unapproved chart repository is
# an unapproved registry one level up.
package rules.supplier_charts

import data.lib
import rego.v1

flux contains f if {
	some f in lib.under("clusters/")
	endswith(f.path, ".yaml")
}

list := [f.contents | some f in lib.at("scripts/approved-suppliers.yml")][0]

# The repository's own address, from the source its Flux reads it from.
own := $"{m[1]}/{m[2]}" if {
	some f in lib.objects
	f.contents.kind == "GitRepository"
	f.contents.metadata.name == "flux-system"
	some m in regex.find_all_string_submatch_n(`^https://github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+?)(?:\.git)?$`, f.contents.spec.url, 1)
}

sources contains [f.path, u] if {
	some f in flux
	walk(f.contents, [path, u])
	count(path) > 0
	path[count(path) - 1] == "url"
	is_string(u)
	regex.match(`^(?:oci|https)://[^\s"']+$`, u)
}

charted contains [path, u] if {
	some [path, u] in sources
	not contains(u, own)
	not startswith(u, "oci://${RELEASE_REPOSITORY}-")
}

deny contains msg if {
	not own
	msg := "the flux-system GitRepository names no repository on GitHub, so the repository's own address is unknown"
}

deny contains msg if {
	count(charted) == 0
	msg := "no chart sources found under clusters/, so this rule proves nothing"
}

deny contains msg if {
	some [path, u] in charted
	not u in {ch.url | some ch in object.get(list, "charts", [])}
	msg := $"{path}: chart source \"{u}\" is not an approved supplier. Add it to scripts/approved-suppliers.yml with a reason."
}
