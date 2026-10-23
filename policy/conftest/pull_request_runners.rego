# A pull request's code never runs on the estate's runner (#554).
#
# The management pool is a machine on the estate's own network, holding the
# credentials that converge real infrastructure. Everything a pull request
# can run is supposed to reach nothing of value - that is what makes the
# pull request's approval the only gate this estate needs.
#
# It used to be enforced the other way round. The estate's plan ran pull
# request code on that runner, so the guard demanded a fork guard and an
# environment on every such job, and a required reviewer on the environment
# was what made a branch pushed to this repository wait for a human. Three
# things were wrong with that: it put a click on every plan; the guard could
# never see whether the environment actually had a reviewer, and one named
# here without one was open (staging was); and it made every collaborator,
# bot and leaked token one unreviewed branch away from the vault token.
#
# Plans now run against the as-built record on GitHub's runner and hold no
# credential, so the rule can be the plain one: a pull-request job runs on a
# GitHub-hosted runner, or in a pool the suppliers list declares under
# pull_request_pools - one with no secret and no route to the estate.
# Anything else is refused, the management pool by name and any runner this
# cannot identify by default, because a label nobody declared is a runner
# nobody vetted.
package rules.pull_request_runners

import data.lib
import rego.v1

pools := {sprintf("pr-%s-${{ vars.pull_request_runners_site }}", [name]) |
	some f in lib.at("scripts/approved-suppliers.yml")
	some name in f.contents.pull_request_pools.names
}

# The management pool, by the runner scale set that is it, for the message.
management_pool := name if {
	some f in lib.objects
	f.contents.kind == "HelmRelease"
	f.contents.metadata.name == "self-hosted"
	name := f.contents.spec.values.runnerScaleSetName
}

hosted := `^(ubuntu|windows|macos)-[a-z0-9.-]+$`

# A runner is read as it is written: one name, or each name of a list. An
# expression names no runner this can vet, and is refused as one
# (tests/go/harness RunnersOf).
runners(runs_on) := [runs_on] if is_string(runs_on)

runners(runs_on) := [label | some l in runs_on; label := as_label(l)] if is_array(runs_on)

runners(runs_on) := [] if {
	not is_string(runs_on)
	not is_array(runs_on)
}

as_label(l) := l if is_string(l)

as_label(l) := "" if not is_string(l)

safe(label) if regex.match(hosted, label)

safe(label) if label in pools

pull_request_safe(runs_on) if {
	labels := runners(runs_on)
	count(labels) > 0
	every label in labels {
		safe(label)
	}
}

# reachable is whether a job's if lets a pull request run it. The first pin
# on a pull-request event decides; pins only on other events keep it out;
# no pin at all lets it in.
event_pin := `github\.event_name\s*(==|!=)\s*'([a-z_]+)'`

pull_request_event(e) if e in {"pull_request", "pull_request_target"}

reachable(cond) if count(regex.find_all_string_submatch_n(event_pin, cond, -1)) == 0

reachable(cond) if {
	pins := [m | some m in regex.find_all_string_submatch_n(event_pin, cond, -1); pull_request_event(m[2])]
	count(pins) > 0
	pins[0][1] == "=="
}

on_pull_request(doc) if {
	some e in lib.triggers(doc)
	pull_request_event(e)
}

examined contains [file, name, job] if {
	some [file, name, job] in lib.jobs
	on_pull_request(lib.workflows[file])
	reachable(object.get(job, "if", ""))
}

shown(runs_on) := runs_on if {
	is_string(runs_on)
	runs_on != ""
}

shown(runs_on) := "a list, a group or an expression" if not is_string(runs_on)

deny contains msg if {
	some [file, name, job] in examined
	runs_on := object.get(job, "runs-on", null)
	not pull_request_safe(runs_on)
	msg := sprintf("%s: job %q runs pull-request code on %s. A pull request's code runs on a GitHub-hosted runner, or in a pool the suppliers list declares under pull_request_pools: one with no secret and no route to the estate. The management pool (%s) converges real infrastructure and is reached from main only. A fork guard or an environment does not change that: a branch pushed to this repository passes the first, and an environment is only a gate if somebody remembers to give it a reviewer.", [file, name, shown(runs_on), object.get({"m": management_pool}, "m", "unread")])
}

deny contains msg if {
	count(lib.workflows) < 5
	msg := sprintf("only %d workflows were read; this rule is reading the wrong directory", [count(lib.workflows)])
}

deny contains msg if {
	count(examined) == 0
	msg := "no pull-request job was found at all, so this checked nothing: the workflows moved, or the reader stopped matching them"
}

deny contains msg if {
	count(pools) == 0
	msg := "the suppliers list declares no pull-request pool under pull_request_pools.names, so every pool would be refused for want of a name"
}

deny contains msg if {
	not management_pool
	msg := "the management pool's name could not be read from its HelmRelease (self-hosted), so a refusal could not name it"
}
