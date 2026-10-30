# A dashboard reaches Grafana as it was written, and says what each picture
# is for.
#
# Two ways one fails without anything saying so. It holds a variable: Flux
# substitutes into everything it applies, and the lane that validates
# substitutes a bare "$name" too, so a dashboard's own variable is blanked
# on the way in and its panels are empty for no reason anybody can see. And
# it is a picture with no account of itself, which is how
# monitoring becomes decoration - this epoch's own words for it. That it is handed to Grafana at all is
# rules.dashboards_listed.
package rules.dashboards

import data.lib
import rego.v1

# substituted is a "$" that something on a dashboard's way to Grafana would
# take for a variable of its own: Flux takes the braced form, and the lane
# that validates takes a bare name too.
substituted := `\$(\{|[A-Za-z_])\w*`

# A dashboard is a JSON file Flux applies that has panels and a schema
# version.
declared contains f if {
	some f in lib.under("clusters/")
	endswith(f.path, ".json")
	is_object(f.contents)
	f.contents.panels
	f.contents.schemaVersion
}

base(path) := parts[count(parts) - 1] if parts := split(path, "/")

text(of, key) := trim_space(s) if {
	s := object.get(of, key, "")
	is_string(s)
}

text(of, key) := "" if not is_string(object.get(of, key, ""))

asks(p) if {
	some target in object.get(p, "targets", [])
	text(target, "expr") != ""
}

deny contains msg if {
	count(declared) == 0
	msg := "no dashboard was found in what Flux applies, so this checked nothing. Criterion 19 of epoch 04 asks for one for each question the epoch asks."
}

deny contains msg if {
	some f in declared
	walk(f.contents, [path, v])
	some s in array.concat([v | is_string(v)], [k | some k in path; is_string(k)])
	found := regex.find_n(substituted, s, 1)
	count(found) > 0
	msg := $"{f.path} holds \"{found[0]}\". What Flux applies is substituted into on its way in, so a dashboard's own variable arrives blank. Write the panel with no variable."
}

deny contains msg if {
	some f in declared
	uid := text(f.contents, "uid")
	uid != trim_suffix(base(f.path), ".json")
	msg := $"{f.path} has the uid \"{uid}\". A dashboard is linked to by its uid, so it is the file's own name and does not change when the title does."
}

deny contains msg if {
	some f in declared
	count(f.contents.panels) == 0
	msg := $"{f.path} has no panel"
}

deny contains msg if {
	some f in declared
	some p in f.contents.panels
	text(p, "title") == ""
	msg := $"{f.path} has a panel with no title"
}

deny contains msg if {
	some f in declared
	some p in f.contents.panels
	text(p, "title") != ""
	text(p, "description") == ""
	msg := $"{f.path}: the panel \"{text(p, "title")}\" does not say what it is for. A picture with no account of what reading means what is one nobody acts on."
}

deny contains msg if {
	some f in declared
	some p in f.contents.panels
	text(p, "title") != ""
	text(p, "description") != ""
	not asks(p)
	msg := $"{f.path}: the panel \"{text(p, "title")}\" asks Prometheus nothing"
}
