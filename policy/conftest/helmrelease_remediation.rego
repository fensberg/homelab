# A HelmRelease retries rather than stalling for a person.
#
# Flux's default strategy gives up after a finite number of attempts, sets
# Stalled, and then does nothing until somebody forces a reconcile by hand -
# a person at a keyboard for a blocker which has usually cleared on its own by
# the time they get there. The monitoring stack could not be admitted until
# the namespace's Pod Security label arrived from OpenTofu; its attempts were
# spent long before that; and every Flux layer behind it stopped until the
# operator was walked through an annotation at a terminal (#459).
#
# The fix is NOT unlimited remediation retries. Install remediation is an
# uninstall between every attempt, so `retries: -1` on a permanently broken
# release uninstalls and reinstalls it indefinitely, and the API documents no
# backoff on that path.
#
# RetryOnFailure retries on a fixed interval and applies no remediation. An
# ordering problem heals itself; a release that is really broken retries
# quietly and shows as a sustained Ready=False, which monitoring can see.
package helmrelease_remediation

import rego.v1

releases contains f if {
	some f in input
	f.contents.kind == "HelmRelease"
}

strategy(f, phase) := object.get(object.get(object.get(f.contents, "spec", {}), phase, {}), ["strategy", "name"], "")

deny contains msg if {
	some f in releases
	some phase in ["install", "upgrade"]
	strategy(f, phase) != "RetryOnFailure"
	msg := sprintf("%s: HelmRelease %s declares spec.%s.strategy.name = %q, not RetryOnFailure. Any other setting gives up after a finite number of attempts and waits for a person to force a reconcile, while every Flux layer behind it waits too (#459). Unlimited remediation retries are not the answer either: install remediation uninstalls between attempts, so a permanently broken release would tear itself down and rebuild forever.", [f.path, f.contents.metadata.name, phase, strategy(f, phase)])
}

deny contains msg if {
	count(releases) == 0
	msg := "no HelmRelease was found, so this checked nothing - either they have moved or this has stopped parsing them"
}
