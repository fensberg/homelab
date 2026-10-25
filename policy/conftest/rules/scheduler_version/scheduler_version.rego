# The scheduler a change is tried with is the scheduler a site runs.
#
# The engineer's answer is only the site's answer if the two are the same
# program. A change to how the scheduler places work arrives with a version
# of Kubernetes, and a trial left on the old one would go on stamping what
# the site itself no longer would. The two are pinned in two files for two
# tools - a site's machines take theirs from the cluster's module, and a
# delivery takes its from versions.env - so this holds them equal.
package rules.scheduler_version

import data.lib
import rego.v1

pins := [f.contents | some f in lib.at("scripts/versions.env")][0]

# What the cluster's module says a site runs, as a local.
declared(key) := {[f.path, v] |
	some f in lib.opentofu
	some block in object.get(f.contents, "locals", [])
	v := block[key]
	is_string(v)
}

deny contains msg if {
	not regex.match(`^\d+\.\d+\.\d+$`, object.get(pins, "KUBERNETES_VERSION", ""))
	msg := "scripts/versions.env pins no KUBERNETES_VERSION=X.Y.Z, so nothing says which scheduler a change is tried with"
}

deny contains msg if {
	tried := pins.KUBERNETES_VERSION
	some [path, site] in declared("kubernetes_version")
	tried != site
	msg := $"a change is tried with Kubernetes {tried} and a site runs {site} ({path}). The engineer stamps a change on the word of a scheduler, and it has to be the site's. Move KUBERNETES_VERSION in scripts/versions.env to {site} and run task order-deliveries, in the change that moves the site."
}
