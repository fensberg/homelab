# The resolvers are declared once and read everywhere.
#
# They were written out four times - twice in compute.tf's cloud-init and twice
# in talos.tf's ResolverConfig (#360). Four copies of one value is the exact
# duplication scripts/versions.env exists to refuse for tool versions, and it
# fails the same way: three of them get updated and the fourth machine class
# resolves through something nobody meant.
#
# It is also the estate's only documented route for a fork on a network with
# internal resolvers or a policy against public upstreams. One line to change
# is a route; four lines to find is a bug waiting.
package rules.dns_resolvers

import data.lib
import rego.v1

tf contains f if {
	some f in lib.opentofu
}

declarations contains [f.path, block] if {
	some f in tf
	some block in object.get(f.contents, "locals", [])
	block.dns_resolvers
}

ipv4 := `^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$`

about_resolving(key) if regex.match(`dns|nameserver|servers`, key)

declared_at(path) if {
	path[0] == "locals"
	path[2] == "dns_resolvers"
}

# A resolver written as a value, under a key that says what it is for.
written contains [f.path, key, v] if {
	some f in tf
	walk(f.contents, [path, v])
	is_string(v)
	regex.match(ipv4, v)
	some key in path
	is_string(key)
	about_resolving(key)
	not declared_at(path)
}

# Or written inside an expression OpenTofu renders - a cloud-init or a machine
# configuration - on a line that says what it is for. A line that is a comment
# is prose about the decision, not the decision.
written contains [f.path, trim_space(line), m[1]] if {
	some f in tf
	walk(f.contents, [_, v])
	is_string(v)
	some line in split(v, "\n")
	not regex.match(`^\s*(#|//)`, line)
	about_resolving(line)
	some m in regex.find_all_string_submatch_n(`"(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3})"`, line, -1)
}

deny contains msg if {
	count(declarations) != 1
	msg := $"local.dns_resolvers is declared {count(declarations)} time(s); it has to be exactly one, or it is not a single source"
}

deny contains msg if {
	some [path, where, address] in written
	msg := $"{path} names the resolver {address} directly ({where}). The resolvers are declared once, as local.dns_resolvers, because this value was restated four times and a fork changing it had to find all four. Read it from there."
}

deny contains msg if {
	count(tf) < 10
	msg := $"only {count(tf)} OpenTofu file(s) were read, so the enumeration has stopped matching"
}
