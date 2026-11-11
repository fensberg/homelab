# Every Dependabot ecosystem groups its updates, and a Go one takes every
# dependency.
#
# Ungrouped, every bump is its own pull request and they conflict with each
# other over the lockfile. A group takes everything, for minor and patch, and
# version updates only: a security update arrives alone and unblocked, and a
# major is read on its own rather than bundled with patch bumps.
package rules.dependabot

import data.lib
import rego.v1

config := ".github/dependabot.yml"

updates := [u | some f in lib.at(config); some u in object.get(f.contents, "updates", [])]

# An entry names one directory or several; it is named by whichever.
where(u) := $"{u["package-ecosystem"]} in {u.directory}" if u.directory

where(u) := $"{u["package-ecosystem"]} in {concat(", ", u.directories)}" if not u.directory

# A Go entry takes every dependency, not only what a go.mod requires
# directly. One taken in through another had a fixed vulnerability and got no
# pull request (#864).
takes_every_dependency(u) if {
	some a in object.get(u, "allow", [])
	a["dependency-type"] == "all"
}

deny contains msg if {
	count(updates) == 0
	msg := $"{config} declares no ecosystems, so this rule would pass having read nothing"
}

deny contains msg if {
	some u in updates
	count(object.get(u, "groups", {})) == 0
	msg := $"{where(u)} declares no groups: every bump becomes its own pull request, and they will conflict with each other over the lockfile. Add a groups: block taking patterns [\"*\"] for minor and patch."
}

deny contains msg if {
	some u in updates
	some name, g in object.get(u, "groups", {})
	object.get(g, "applies-to", "") != "version-updates"
	msg := $"{where(u)} group \"{name}\" has applies-to \"{object.get(g, "applies-to", "")}\", want \"version-updates\": without it the group also swallows security updates, which should arrive alone and unblocked."
}

deny contains msg if {
	some u in updates
	some name, g in object.get(u, "groups", {})
	object.get(g, "patterns", []) != ["*"]
	msg := $"{where(u)} group \"{name}\" has patterns {object.get(g, "patterns", [])}, want [\"*\"]: anything the group does not match gets its own pull request and conflicts with the group's."
}

deny contains msg if {
	some u in updates
	some name, g in object.get(u, "groups", {})
	count(object.get(g, "update-types", [])) == 0
	msg := $"{where(u)} group \"{name}\" names no update-types: it therefore also groups majors, and a breaking change should be read on its own rather than bundled with patch bumps."
}

deny contains msg if {
	some u in updates
	some name, g in object.get(u, "groups", {})
	"major" in object.get(g, "update-types", [])
	msg := $"{where(u)} group \"{name}\" includes update-type \"major\": a breaking change should arrive as its own pull request."
}

deny contains msg if {
	some u in updates
	u["package-ecosystem"] == "gomod"
	not takes_every_dependency(u)
	msg := $"{where(u)} bumps only what a go.mod requires directly: a vulnerability fixed in a module taken in through another gets no pull request. Add an allow entry of dependency-type all."
}
