# A site runs the release of the platform it was given, and no other way.
#
# WHY. A site's roots build nothing; they call modules. Called by path, every
# site runs the modules as they stand on the commit being converged, so a
# change reaches every site on the merge that made it and there is no way to
# try it on one first. A release is what a merge publishes instead, and each
# site's line in management/versions.json says which release that site runs:
# merging code changes nothing a site runs until its line moves.
#
# That only holds if a root cannot reach its modules around the release. So
# every module a site's root calls comes from the tree it was handed - the
# release, or for a check the same path in a checkout - nothing chooses a
# release by default, and every site there is has a line. That a module is
# one the release holds needs no rule: the checkout a plan is handed is
# placed from what the release holds, so a module it lacks is not there to
# plan.
#
# ON WHAT TOFU WOULD MAKE. The calls are read off each site's plan, made with
# a checkout handed in as the unreleased tree, so a call that comes from that
# tree came through the one form that reads it. That the form fetches the
# release by digest, with the same path, is how the code is written, and
# policy/semgrep/released-modules.yml holds it.
package rules.plan_released_modules

import data.lib
import rego.v1

handed := {"release", "digest", "unreleased"}

site_plans contains path if {
	some [path, _, _, _] in lib.plan_variables
	lib.plan_scope(path) == "site"
}

checkout(path) := given if {
	some [p, "unreleased", _, given] in lib.plan_variables
	p == path
	is_string(given)
	given != ""
}

lines := object.union_n([f.contents | some f in lib.at("management/versions.json")])

sites := object.union_n([f.contents.sites | some f in lib.at("config/management.tpl.json")])

deny contains msg if {
	some path in site_plans
	some name in handed
	count({n | some [p, n, _, _] in lib.plan_variables; p == path; n == name}) == 0
	msg := $"{path} declares no \"{name}\" variable, so nothing tells it where its modules come from"
}

deny contains msg if {
	some [path, name, declared, _] in lib.plan_variables
	path in site_plans
	name in handed
	default_of := object.get(declared, "default", "")
	default_of != ""
	msg := $"{path} gives \"{name}\" a default of {default_of}. A root that was not told which release to run must run none; a default is a version nobody chose"
}

deny contains msg if {
	some [path, name, call] in lib.plan_module_calls
	path in site_plans
	not startswith(call.source, $"{checkout(path)}/")
	msg := $"{path} calls the module {name} from {call.source}, and not from the tree it was handed. A site's root fetches every module from the release its site was given; any other source runs something no site's line chose"
}

deny contains msg if {
	some path in site_plans
	count({n | some [p, n, _] in lib.plan_module_calls; p == path}) == 0
	msg := $"{path} calls no module, so either its root builds something itself or this has stopped reading it"
}

deny contains msg if {
	some path in site_plans
	not checkout(path)
	msg := $"{path} was made with no unreleased tree, so where its modules came from cannot be read off it"
}

deny contains msg if {
	some site, _ in sites
	not lines[site]
	msg := $"{site} is a site, and management/versions.json has no line for it, so no run against it can start. Give it the release it runs"
}

deny contains msg if {
	some site, _ in lines
	not sites[site]
	msg := $"management/versions.json gives {site} a release, and the config declares no such site. A line that is nobody's is a version somebody believes is running"
}

deny contains msg if {
	count(site_plans) == 0
	msg := "no site's plan was read, so no site's modules were held to its release"
}

deny contains msg if {
	count(sites) == 0
	msg := "the config template declares no site"
}
