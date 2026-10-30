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
package rules.rclone_namespace

import data.lib
import rego.v1

pins := [f.contents | some f in lib.at("scripts/versions.env")][0]

inert := "RCLONE_DEB_VERSION"

deny contains msg if {
	not pins[inert]
	msg := $"scripts/versions.env no longer declares {inert}. That is the rclone pin. install-dependencies.sh and the runner image both read it, and without it the workstation and the runner install whatever Debian has. If it was renamed, rename it to something outside the RCLONE_ namespace and update this rule - do not put it back as RCLONE_VERSION."
}

deny contains msg if {
	some key, _ in pins
	startswith(key, "RCLONE_")
	key != inert
	flag := concat("", ["--", lower(replace(trim_prefix(key, "RCLONE_"), "_", "-"))])
	msg := $"scripts/versions.env declares {key}, which is in rclone's own configuration namespace. .github/actions/versions exports every pin into the job environment, and rclone reads RCLONE_<FLAG> as {flag}. If rclone has that flag, every rclone call in every job that reads the pins fails before doing any work - which is what RCLONE_VERSION did to five nights of backups and integration runs (#487). Name the pin outside the namespace. {inert} is the one spelling checked to be inert."
}
