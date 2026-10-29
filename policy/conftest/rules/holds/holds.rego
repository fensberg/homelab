# Everything that keeps data says what it holds, and for how long.
#
# The safety officer clears a teardown by reading what the site holds that
# is worth keeping (scripts/details/holds), so it knows only what it is
# told. An application or a controller that takes a volume and declares
# nothing is, to the officer, holding nothing: the teardown is cleared and
# the data goes with it. So every object in the repository's manifests that
# declares storage has to be named by a `holds` entry of whoever owns it -
# the application whose directory it is in, or the core - and an entry that
# names something no longer here is refused too, because it is a promise
# about nothing.
#
# AND WHERE IT LIVES IS WHERE ITS STORAGE PUTS IT. A volume on a class the
# storage driver provides is kept on the node and outlives any machine; a
# volume on any other class is on a machine's own disk and goes with it. A
# declaration that says otherwise is the one the safety officer would
# believe.
package rules.holds

import data.lib
import rego.v1

core := "core"

# Every holds entry, as [owner, entry]: the core's, and each application's,
# by the directory its declaration is in.
declared contains [core, entry] if {
	some f in lib.at("clusters/core/holds.json")
	some entry in f.contents.holds
}

declared contains [name, entry] if {
	some [name, app] in applications
	some entry in object.get(app, "holds", [])
}

applications contains [parts[2], f.contents] if {
	some f in lib.named("application.json")
	parts := split(f.path, "/")
	count(parts) == 4
	parts[0] == "modules"
	parts[1] == "applications"
}

owner(path) := parts[2] if {
	parts := split(path, "/")
	count(parts) > 3
	parts[0] == "modules"
	parts[1] == "applications"
}

owner(path) := core if not startswith(path, "modules/applications/")

# Every object that asks for a volume, as [path, owner, kind/name, classes]:
# a claim, a database cluster that says where its data goes, and anything
# carrying a claim template at any depth - a StatefulSet, or a chart's values
# asking for one.
keepers contains [f.path, owner(f.path), $"{f.contents.kind}/{f.contents.metadata.name}", classes_of(f.contents)] if {
	some f in lib.objects
	is_string(f.contents.metadata.name)
	keeps(f.contents)
}

keeps(doc) if doc.kind == "PersistentVolumeClaim"

keeps(doc) if {
	doc.kind == "Cluster"
	doc.spec.storage
}

keeps(doc) if {
	walk(doc, [path, _])
	count(path) > 0
	path[count(path) - 1] in {"volumeClaimTemplate", "volumeClaimTemplates"}
}

classes_of(doc) := {v |
	walk(doc, [path, v])
	count(path) > 0
	path[count(path) - 1] in {"storageClassName", "storageClass"}
	is_string(v)
	v != ""
}

# The classes the storage driver provides, by its release: the chart it
# installs, and the classes its values declare.
outlive_a_machine contains c.name if {
	some f in lib.objects
	f.contents.kind == "HelmRelease"
	f.contents.spec.chart.spec.chart == "proxmox-csi-plugin"
	some c in f.contents.spec.values.storageClass
}

lives_on(class) := "node" if class in outlive_a_machine

lives_on(class) := "machine" if not class in outlive_a_machine

deny contains msg if {
	some [path, who, held, _] in keepers
	count({e | some [who_declared, e] in declared; who_declared == who; e.held_by == held}) == 0
	msg := $"{path} declares storage ({held}) and nothing says what it holds. {who} owns it, and its declaration has no holds entry with \"held_by\": \"{held}\". Until it does the safety officer reads it as holding nothing, and clears a teardown that takes whatever is on it. Say what it is, the scope it is worth keeping for the life of, and the scope its working copy dies with."
}

deny contains msg if {
	some [who, entry] in declared
	count({p | some [p, kept_by, held, _] in keepers; kept_by == who; held == entry.held_by}) == 0
	msg := $"{who} says it holds something in {entry.held_by}, and nothing of its own declares storage by that kind and name"
}

deny contains msg if {
	some [path, who, held, classes] in keepers
	some [who_declared, entry] in declared
	who_declared == who
	entry.held_by == held
	some class in classes
	lives_on(class) != entry.lives_on
	msg := $"{path} keeps {held} on the class \"{class}\", which lives on a {lives_on(class)}, and {who} declares it lives on a {entry.lives_on}. The safety officer decides whether destroying a machine endangers it from that declaration: said to live on a node when it is on a machine's disk, it is cleared for a retirement that takes it; the other way round, a retirement is refused over something that was never at risk."
}

deny contains msg if {
	some [name, app] in applications
	some keys in object.get(app, "secrets", {})
	some source in keys
	source.storage
	count({e | some e in object.get(app, "holds", []); e.copy}) == 0
	msg := $"modules/applications/{name} is given a key to a bucket and holds nothing with a copy, so nothing says what it puts there or how old that may be"
}

deny contains msg if {
	count(keepers) == 0
	msg := "found nothing in the repository that declares storage, so this looked for nothing"
}

deny contains msg if {
	count(outlive_a_machine) == 0
	msg := "the storage driver's release declares no class, so nothing could be said to outlive a machine"
}
