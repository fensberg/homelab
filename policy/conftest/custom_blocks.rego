# A resource declared in an OpenTofu root is refused unless somebody says why.
#
# The rule (docs/epochs/02-abstraction.md): reuse the reusable block that
# exists, or build a reusable one, and only build something custom as a
# decision made out loud. This cannot judge reusability. It refuses what is
# undeclared, so an omission and a considered exception never look the same,
# and it refuses a declaration that no longer matches anything, so the list
# cannot fill with exceptions for things that are gone. Declarations live in
# tests/custom-blocks.yml.
#
# A root is providers, credentials and state; what it builds is a module's,
# so a second site is the same module called again. Roots are found by what a
# root is - a directory holding a provider block - so the next one is held to
# this without being listed. (The Kubernetes and Go halves of the same rule
# are guards in tests/go/repo.)
package custom_blocks

import rego.v1

declarations := "tests/custom-blocks.yml"

dir(path) := concat("/", array.slice(parts, 0, count(parts) - 1)) if parts := split(path, "/")

tofu contains f if {
	some f in input
	endswith(f.path, ".tf")
	not contains(f.path, "/tests/")
}

roots contains dir(f.path) if {
	some f in tofu
	count(object.get(f.contents, "provider", {})) > 0
}

found contains sprintf("%s.%s", [type, name]) if {
	some f in tofu
	dir(f.path) in roots
	some type, named in object.get(f.contents, "resource", {})
	some name, _ in named
}

declared := [d | some f in input; f.path == declarations; some d in object.get(f.contents, "opentofu", [])]

deny contains msg if {
	count(roots) < 3
	msg := sprintf("found %d OpenTofu root(s), so the enumeration has stopped matching", [count(roots)])
}

deny contains msg if {
	some d in declared
	trim_space(object.get(d, "reason", "")) == ""
	msg := sprintf("%s declares %q with no reason; the reason is the whole price of a custom block", [declarations, d.resource])
}

deny contains msg if {
	some r in found
	not r in {d.resource | some d in declared}
	msg := sprintf("OpenTofu resource %q is declared in a root rather than in a module. A root holds providers, credentials and state; what it builds goes in a module under modules/infrastructure/, so the next site calls the same block. Move it, or declare it in %s with the reason it can have only one caller.", [r, declarations])
}

deny contains msg if {
	some d in declared
	not d.resource in found
	msg := sprintf("%s declares %q, and no such OpenTofu resource exists any more. Remove the declaration, so the list holds only exceptions that are real.", [declarations, d.resource])
}
