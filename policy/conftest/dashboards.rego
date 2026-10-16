# substituted is a "$" that something on a dashboard's way to Grafana would
# take for a variable of its own: Flux takes the braced form, and the lane
# that validates takes a bare name too.
# dashboards is every dashboard in what Flux applies, by its path. Found by
# what it is - a JSON file holding panels - and not by where it is kept.
# A dashboard is kept in git, reaches Grafana, and says what each picture is
# for.
#
# Three ways one fails without anything saying so. It holds a variable: Flux
# substitutes into everything it applies, and the lane that validates
# substitutes a bare "$name" too, so a dashboard's own variable is blanked
# on the way in and its panels are empty for no reason anybody can see. It
# is written and never handed over: a file nothing lists is a dashboard that
# is not there. And it is a picture with no account of itself, which is how
# monitoring becomes decoration - this epoch's own words for it.
package dashboards

import rego.v1

substituted := `\$(\{|[A-Za-z_])\w*`

# A dashboard is a JSON file Flux applies that has panels and a schema
# version.
dashboards contains f if {
	some f in input
	startswith(f.path, "clusters/")
	endswith(f.path, ".json")
	is_object(f.contents)
	f.contents.panels
	f.contents.schemaVersion
}

dir(path) := concat("/", array.slice(parts, 0, count(parts) - 1)) if parts := split(path, "/")

base(path) := parts[count(parts) - 1] if parts := split(path, "/")

text(of, key) := trim_space(s) if {
	s := object.get(of, key, "")
	is_string(s)
}

text(of, key) := "" if not is_string(object.get(of, key, ""))

deny contains msg if {
	count(dashboards) == 0
	msg := "no dashboard was found in what Flux applies, so this checked nothing. Criterion 19 of epoch 04 asks for one for each question the epoch asks."
}

deny contains msg if {
	some f in dashboards
	walk(f.contents, [path, v])
	some s in array.concat([v | is_string(v)], [k | some k in path; is_string(k)])
	found := regex.find_n(substituted, s, 1)
	count(found) > 0
	msg := sprintf("%s holds %q. What Flux applies is substituted into on its way in, so a dashboard's own variable arrives blank. Write the panel with no variable.", [f.path, found[0]])
}

deny contains msg if {
	some f in dashboards
	uid := text(f.contents, "uid")
	uid != trim_suffix(base(f.path), ".json")
	msg := sprintf("%s has the uid %q. A dashboard is linked to by its uid, so it is the file's own name and does not change when the title does.", [f.path, uid])
}

deny contains msg if {
	some f in dashboards
	count(f.contents.panels) == 0
	msg := sprintf("%s has no panel", [f.path])
}

asks(p) if {
	some target in object.get(p, "targets", [])
	text(target, "expr") != ""
}

deny contains msg if {
	some f in dashboards
	some p in f.contents.panels
	text(p, "title") == ""
	msg := sprintf("%s has a panel with no title", [f.path])
}

deny contains msg if {
	some f in dashboards
	some p in f.contents.panels
	text(p, "title") != ""
	text(p, "description") == ""
	msg := sprintf("%s: the panel %q does not say what it is for. A picture with no account of what reading means what is one nobody acts on.", [f.path, text(p, "title")])
}

deny contains msg if {
	some f in dashboards
	some p in f.contents.panels
	text(p, "title") != ""
	text(p, "description") != ""
	not asks(p)
	msg := sprintf("%s: the panel %q asks Prometheus nothing", [f.path, text(p, "title")])
}

# The kustomization beside a dashboard's directory lists it, and labels what
# it generates for the sidecar that hands dashboards to Grafana.
listing(f) := k if {
	some k in input
	k.path == sprintf("%s/kustomization.yaml", [dir(dir(f.path))])
}

deny contains msg if {
	some f in dashboards
	not listing(f)
	msg := sprintf("%s is not handed to Grafana: no kustomization is beside its directory to list it", [f.path])
}

deny contains msg if {
	some f in dashboards
	k := listing(f)
	want := sprintf("%s/%s", [base(dir(f.path)), base(f.path)])
	not want in {file | some g in object.get(k.contents, "configMapGenerator", []); some file in object.get(g, "files", [])}
	msg := sprintf("%s is not handed to Grafana: the kustomization beside its directory does not list it, so it is a file and not a dashboard.", [f.path])
}

deny contains msg if {
	some f in dashboards
	k := listing(f)
	object.get(k.contents, ["generatorOptions", "labels", "grafana_dashboard"], "") == ""
	msg := sprintf("%s is listed and not labelled grafana_dashboard, so the sidecar that hands dashboards to Grafana does not see it.", [f.path])
}
