# A site rings the watchman exactly as often as the watchman looks round.
#
# How often is one fact held in two places that cannot read each other. The
# watchman's own figure is where the estate declares it, and it is what the
# length of a silence and the vendor's free plan are both worked out from:
# two rings missed is a site gone quiet, and each ring is a write. How often
# a site actually rings is two intervals in its Alertmanager's route. A site
# that rang less often would be called quiet while it was well; one that
# rang more often would spend more of the free plan than the estate counted
# when it refused to go over.
#
# Alertmanager looks at a group every group_interval and sends again once
# repeat_interval has passed. So the ring goes out on the first look at or
# after the repeat - and a repeat that is exactly some number of looks is
# refused, because a look landing a moment early waits a whole look more.
package rules.plan_heartbeat_cadence

import data.lib
import rego.v1

receiver := "watchman"

stacks := [f | some f in lib.objects; f.contents.kind == "HelmRelease"; f.contents.metadata.name == "kube-prometheus-stack"]

routes contains [f.path, r] if {
	some f in stacks
	some r in object.get(f.contents.spec.values.alertmanager.config.route, "routes", [])
	r.receiver == receiver
}

# schedules is every schedule the watchman is looked round on, as the
# estate's plan would set it, by its trigger.
schedules contains [r.address, s.cron] if {
	some [_, r] in lib.plan
	r.type == "cloudflare_workers_cron_trigger"
	some s in object.get(r.values, "schedules", [])
}

# declared is how often the watchman looks round, in minutes: a schedule
# of every so many minutes.
declared contains [address, to_number(n)] if {
	some [address, cron] in schedules
	[_, n] := regex.find_all_string_submatch_n(`^\*/(\d+) \* \* \* \*$`, cron, 1)[0]
}

minutes(written) := to_number(trim_suffix(written, "m")) if regex.match(`^\d+m$`, written)

deny contains msg if {
	count(routes) == 0
	msg := sprintf("the monitoring stack sends nothing to the %s, so no site rings it and it will never notice one stop", [receiver])
}

deny contains msg if {
	some [path, r] in routes
	some what in ["group_interval", "repeat_interval"]
	not minutes(object.get(r, what, ""))
	msg := sprintf("%s: the route to the %s has a %s of %q, which is not a number of minutes, so how often it rings cannot be worked out", [path, receiver, what, object.get(r, what, "")])
}

deny contains msg if {
	some [path, r] in routes
	looks := minutes(r.group_interval)
	repeat := minutes(r.repeat_interval)
	repeat % looks == 0
	msg := sprintf("%s: the route to the %s repeats every %dm and is looked at every %dm, which is exactly %d looks. A look that lands a moment early finds the repeat not yet passed and waits a whole look more, so the site rings late about as often as not. Make the repeat a little less than the looks it should take.", [path, receiver, repeat, looks, repeat / looks])
}

deny contains msg if {
	count(schedules) != 1
	msg := sprintf("the %s is looked round on %d schedules in the estate's plan, and how often it looks is one fact", [receiver, count(schedules)])
}

deny contains msg if {
	some [address, cron] in schedules
	not address in {a | some [a, _] in declared}
	msg := sprintf("%s looks the %s round on %q, which is not looked round every so many minutes, so how often it looks cannot be read", [address, receiver, cron])
}

deny contains msg if {
	some [path, r] in routes
	looks := minutes(r.group_interval)
	repeat := minutes(r.repeat_interval)
	rings := ceil(repeat / looks) * looks
	some [declared_in, expected] in declared
	is_number(expected)
	rings != expected
	msg := sprintf("a site rings the %s every %d minutes (%s: looked at every %dm, repeated after %dm), and the %s looks round every %d (%s). How long a silence is before it is said, and what a site costs on the vendor's free plan, are both worked out from the second figure.", [receiver, rings, path, looks, repeat, receiver, expected, declared_in])
}
