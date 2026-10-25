# A root reaches files outside itself from the repository's top.
#
# A file that reads something outside its own directory names it from the
# top of the repository, by climbing exactly as far as its directory is deep.
#
# A plan against the as-built record runs a copy of a root, placed at the
# root's own depth so that its way up still leads to the same file
# (details/asbuilt.copyRoot). Any other way up does not: the copy's parent is
# not management/. That broke the pull-request plan the day the cluster root
# first read management/tunnel-routes.json as "../": the plan refused before
# its first step, on a file that exists. A module is read in place, from
# wherever it is, and the same rule keeps it honest: a path that stops short
# of the top depends on what the module's neighbours happen to be.
#
# Asserted of every OpenTofu file the repository authored, by its own depth,
# so a root or a module at any depth is held to it without being named.
package rules.root_paths

import data.lib
import rego.v1

dir_depth(path) := count(split(path, "/")) - 1

climbs contains [f.path, m[0], count(split(m[1], "/..")) - 1] if {
	some f in lib.opentofu
	walk(f.contents, [_, v])
	is_string(v)
	some m in regex.find_all_string_submatch_n(`\$\{path\.module\}((?:/\.\.)+)`, v, -1)
}

deny contains msg if {
	some [path, how, levels] in climbs
	levels != dir_depth(path)
	msg := $"{path} reaches outside its directory by \"{how}\", and the top of the repository is {dir_depth(path)} level(s) up. A copy of a root planned against the as-built record sits at the same depth elsewhere, so only the way to the top leads to the same file from there. Write the path from the repository's top."
}
