# talosctl is a client for a specific Talos version. Sidero's own guidance is
# to keep it within one minor of the cluster it talks to, and the two are
# pinned in different files by different people at different times - the exact
# shape of drift that goes unnoticed until a command behaves oddly during an
# incident.
#
# This is another gotcha turned into a rule: it was found by reading
# install-dependencies.sh while writing a rotation runbook, not by anything
# failing, which is precisely why it deserves a check rather than a note.
package versions

import data.lib
import rego.v1

pins := [f.contents | some f in input; f.path == "scripts/versions.env"][0]

# What the cluster's module says a site runs, as a local.
declared(key) := {[f.path, v] |
	some f in input
	endswith(f.path, ".tf")
	not contains(f.path, "/tests/")
	some block in object.get(f.contents, "locals", [])
	v := block[key]
	is_string(v)
}

semver(v) := [to_number(m[1]), to_number(m[2]), m[3]] if {
	some m in regex.find_all_string_submatch_n(`^v?(\d+)\.(\d+)\.(\d+)$`, v, 1)
}

deny contains msg if {
	not semver(object.get(pins, "TALOSCTL_VERSION", ""))
	msg := "could not find TALOSCTL_VERSION=vX.Y.Z in scripts/versions.env. If the pin was restructured, this contract needs re-examining rather than re-pointing: something still has to keep the client in step with the cluster."
}

deny contains msg if {
	count(declared("talos_version")) != 1
	msg := sprintf("talos_version is declared as a version %d time(s) where the cluster's module says what a site runs, and it is one fact", [count(declared("talos_version"))])
}

deny contains msg if {
	cli := semver(pins.TALOSCTL_VERSION)
	some [path, v] in declared("talos_version")
	cluster := semver(v)
	not within_a_minor(cli, cluster)
	msg := sprintf("talosctl is pinned to v%d.%d.%s but the cluster runs v%d.%d.%s (%s). Sidero supports a client within one minor of the cluster. Two apart is where commands start behaving differently from their documentation - which is a bad thing to discover while running `talosctl rotate-ca` during an incident.", array.concat(array.concat(cli, cluster), [path]))
}

within_a_minor(a, b) if {
	a[0] == b[0]
	abs(a[1] - b[1]) <= 1
}

# kubectl is a client for a specific Kubernetes version, and the project
# supports it within one minor of the cluster in either direction. Same shape
# as the talosctl contract above, same reason: the two numbers live in
# different files and drift without anything failing.
#
# It had drifted, in both directions at once. The cluster ran 1.31.1 pinned
# inline in talos.tf where nothing watched it, versions.env said 1.34.11, and
# the workstation installer ignored the pin and fetched whatever upstream
# called stable - three numbers, no two of them within a minor of each other.

deny contains msg if {
	not semver(object.get(pins, "KUBECTL_VERSION", ""))
	msg := "could not find KUBECTL_VERSION=vX.Y.Z in scripts/versions.env. If the pin was restructured, this contract needs re-examining rather than re-pointing: something still has to keep the client in step with the cluster."
}

deny contains msg if {
	count(declared("kubernetes_version")) != 1
	msg := sprintf("kubernetes_version is declared as a version %d time(s) where the cluster's module says what a site runs. It was inline in talos.tf once, which is how it went five minors stale without anything noticing. It belongs in the locals beside talos_version, because Talos decides which Kubernetes versions are installable at all.", [count(declared("kubernetes_version"))])
}

deny contains msg if {
	cli := semver(pins.KUBECTL_VERSION)
	some [path, v] in declared("kubernetes_version")
	cluster := semver(v)
	not within_a_minor(cli, cluster)
	msg := sprintf("kubectl is pinned to v%d.%d.%s but the cluster runs %d.%d.%s (%s). Kubernetes supports a client within one minor of the API server. Further apart and commands start failing or behaving differently from their documentation, which is a bad thing to discover while draining a node during an incident.", array.concat(array.concat(cli, cluster), [path]))
}

# No workflow restates a version that scripts/versions.env pins.
#
# WHAT THIS GUARDS. versions.env has claimed this test existed since it was
# written - "tests/go/repo/versions_test.go asserts that no consumer restates a
# key" - and it did not (#165). Four workflows carried their own
# `go-version: "1.26.7"`, including deploy-infrastructure, the lane that
# reaches real hardware. They agreed with the pin that day. Nothing kept them
# agreeing, and the same drift had already happened once here: CI validated on
# OpenTofu 1.10.6 for as long as the pin said 1.12.6, because a workflow
# declared its own copy.
#
# The rule is shape, not value: every `<tool>-version:` input to a setup action
# reads the pin through the environment the versions action exports. Checking
# values instead would miss a restatement that happens to agree today, which is
# the exact state all four were in.

setup_input := `^(?:go|node|python|java|dotnet|tofu|opentofu|terraform|helm|kubectl)[-_]version$`

inputs contains [file, key, v] if {
	some file, doc in lib.runnable
	walk(doc, [path, v])
	count(path) > 0
	key := path[count(path) - 1]
	is_string(key)
	regex.match(setup_input, key)
}

deny contains msg if {
	some [file, key, v] in inputs
	not startswith(sprintf("%v", [v]), "${{")
	msg := sprintf("%s restates a pinned version: %s: %v. Read it from scripts/versions.env instead: add the step `uses: ./.github/actions/versions` after the checkout, and write %s: ${{ env.<KEY> }}. A literal agrees with the pin only until the pin moves, and nothing says when it stops agreeing.", [file, key, v, key])
}

deny contains msg if {
	count(inputs) < 2
	msg := sprintf("found only %d version input(s) - the pattern has stopped matching, so this rule proves nothing", [count(inputs)])
}

# A pin must not be readable as configuration by the tool it pins.
#
# WHAT THIS GUARDS. rclone configures every one of its flags from a matching
# RCLONE_<FLAG> environment variable, and .github/actions/versions exports
# every key in versions.env into the job environment of nine workflows. So
# `RCLONE_VERSION=1.75.0` never reached rclone as a version - it reached it as
# `--version=1.75.0`, and --version is a boolean:
#
#   CRITICAL: Invalid value when setting --version from environment variable
#   RCLONE_VERSION="1.75.0": invalid argument "1.75.0" for "--version" flag:
#   strconv.ParseBool: parsing "1.75.0": invalid syntax
#
# Every rclone invocation in the job exits 1 before doing any work. The nightly
# Integration Tests hit it for five nights (#487): the Backup phase halted on
# its first upload, so no encrypted state backup was written, and the
# integration tier - the only check this estate has against a real cluster -
# never ran at all. `backup` is in ConvergePhases too, so a merge-driven deploy
# walks into the same call.
#
# The trap had already been found once, in the runner image, and was closed
# there by aliasing the build argument to RCLONE_DEB_VERSION. Fixing the
# Dockerfile and leaving versions.env alone fixed the build and left the
# environment-export path armed - which is the general shape worth remembering:
# when the same rule is enforced in more than one place, correcting one of them
# is not correcting the rule.
#
# The check is by namespace rather than by spelling. `RCLONE_VERSION` is the
# one that bit, but so would any other key rclone happens to have a flag for,
# and nobody adding a pin is going to cross-check it against rclone's flag set.
# So the whole file is enumerated and anything in the namespace has to be
# declared here deliberately, by somebody who has looked.

inert := "RCLONE_DEB_VERSION"

deny contains msg if {
	not pins[inert]
	msg := sprintf("scripts/versions.env no longer declares %s. That is the rclone pin. install-dependencies.sh and the runner image both read it, and without it the workstation and the runner install whatever Debian has. If it was renamed, rename it to something outside the RCLONE_ namespace and update this rule - do not put it back as RCLONE_VERSION.", [inert])
}

deny contains msg if {
	some key, _ in pins
	startswith(key, "RCLONE_")
	key != inert
	flag := concat("", ["--", lower(replace(trim_prefix(key, "RCLONE_"), "_", "-"))])
	msg := sprintf("scripts/versions.env declares %s, which is in rclone's own configuration namespace. .github/actions/versions exports every pin into the job environment, and rclone reads RCLONE_<FLAG> as %s. If rclone has that flag, every rclone call in every job that reads the pins fails before doing any work - which is what RCLONE_VERSION did to five nights of backups and integration runs (#487). Name the pin outside the namespace. %s is the one spelling checked to be inert.", [key, flag, inert])
}

# The scheduler a change is tried with is the scheduler a site runs.
#
# The engineer's answer is only the site's answer if the two are the same
# program. A change to how the scheduler places work arrives with a version
# of Kubernetes, and a trial left on the old one would go on stamping what
# the site itself no longer would. The two are pinned in two files for two
# tools - a site's machines take theirs from the cluster's module, and a
# delivery takes its from versions.env - so this holds them equal.

deny contains msg if {
	not regex.match(`^\d+\.\d+\.\d+$`, object.get(pins, "KUBERNETES_VERSION", ""))
	msg := "scripts/versions.env pins no KUBERNETES_VERSION=X.Y.Z, so nothing says which scheduler a change is tried with"
}

deny contains msg if {
	tried := pins.KUBERNETES_VERSION
	some [path, site] in declared("kubernetes_version")
	tried != site
	msg := sprintf("a change is tried with Kubernetes %s and a site runs %s (%s). The engineer stamps a change on the word of a scheduler, and it has to be the site's. Move KUBERNETES_VERSION in scripts/versions.env to %s and run task order-deliveries, in the change that moves the site.", [tried, site, path, site])
}
