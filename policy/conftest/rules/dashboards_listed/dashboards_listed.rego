# A dashboard is written and handed to Grafana: the kustomization beside its
# directory lists it, and labels what it generates for the sidecar that hands
# dashboards to Grafana. A file nothing lists is a dashboard that is not there,
# and nothing says so. What a dashboard says of itself is rules.dashboards.
package rules.dashboards_listed

import data.lib
import rego.v1

# A dashboard is a JSON file Flux applies that has panels and a schema
# version.
declared contains f if {
	some f in lib.under("clusters/")
	endswith(f.path, ".json")
	is_object(f.contents)
	f.contents.panels
	f.contents.schemaVersion
}

dir(path) := concat("/", array.slice(parts, 0, count(parts) - 1)) if parts := split(path, "/")

base(path) := parts[count(parts) - 1] if parts := split(path, "/")

listing(f) := k if {
	some k in lib.named("kustomization.yaml")
	k.path == $"{dir(dir(f.path))}/kustomization.yaml"
}

deny contains msg if {
	count(declared) == 0
	msg := "no dashboard was found in what Flux applies, so whether each is handed to Grafana checked nothing"
}

deny contains msg if {
	some f in declared
	not listing(f)
	msg := $"{f.path} is not handed to Grafana: no kustomization is beside its directory to list it"
}

deny contains msg if {
	some f in declared
	k := listing(f)
	want := $"{base(dir(f.path))}/{base(f.path)}"
	not want in {file | some g in object.get(k.contents, "configMapGenerator", []); some file in object.get(g, "files", [])}
	msg := $"{f.path} is not handed to Grafana: the kustomization beside its directory does not list it, so it is a file and not a dashboard."
}

deny contains msg if {
	some f in declared
	k := listing(f)
	object.get(k.contents, ["generatorOptions", "labels", "grafana_dashboard"], "") == ""
	msg := $"{f.path} is listed and not labelled grafana_dashboard, so the sidecar that hands dashboards to Grafana does not see it."
}
