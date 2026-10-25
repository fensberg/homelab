# Every action a workflow uses is one the repository's own settings allow.
#
# The repository is told, in the estate's OpenTofu, which actions may run at
# all - judged on the estate's plan, as it would be applied: a list,
# at a commit, from no publisher merely because the platform verified it.
# Each setting is held to being that wall, and every action a workflow or
# shared action uses is held to getting through it - so a new action is a
# change to the settings, reviewed, before a workflow can run it.
package rules.plan_allowed_actions

import data.lib
import rego.v1

settings contains [r.address, r.values] if {
	some [_, r] in lib.plan
	r.type == "github_actions_repository_permissions"
}

config(s) := object.get(s, "allowed_actions_config", [{}])[0]

patterns(s) := object.get(config(s), "patterns_allowed", [])

# An action is named by its repository: owner/repository, whatever path in it.
used contains action if {
	some [_, _, ref] in lib.uses
	not startswith(ref, "./")
	not startswith(ref, "$/")
	not startswith(ref, "docker://")
	parts := split(split(ref, "@")[0], "/")
	count(parts) >= 2
	action := concat("/", [parts[0], parts[1]])
}

allows(s, action) if {
	config(s).github_owned_allowed == true
	split(action, "/")[0] in {"actions", "github"}
}

allows(s, action) if $"{action}@*" in patterns(s)

deny contains msg if {
	count(used) < 5
	msg := $"only {count(used)} action(s) were found in the workflows, so the allowed-actions rule checked almost nothing"
}

deny contains msg if {
	count(settings) == 0
	msg := "no github_actions_repository_permissions is in any plan, so nothing says what a workflow may run"
}

deny contains msg if {
	some [where, s] in settings
	s.allowed_actions != "selected"
	msg := $"{where}: it does not hold workflows to a list: allowed_actions is not \"selected\""
}

deny contains msg if {
	some [where, s] in settings
	object.get(s, "sha_pinning_required", false) != true
	msg := $"{where}: it lets an action run from a tag or a branch: sha_pinning_required is not true"
}

deny contains msg if {
	some [where, s] in settings
	object.get(config(s), "verified_allowed", true) != false
	msg := $"{where}: it lets in every publisher the platform has verified: verified_allowed is not false"
}

deny contains msg if {
	some [where, s] in settings
	some p in patterns(s)
	not one_action(p)
	msg := $"{where}: it allows {p}, which is wider than one action: write owner/repository@*"
}

one_action(p) if {
	[repo, "*"] = split(p, "@")
	count(split(repo, "/")) == 2
	not contains(repo, "*")
}

deny contains msg if {
	some [where, s] in settings
	some action in used
	not allows(s, action)
	msg := $"{where}: a workflow uses {action}, which it would refuse: add \"{action}@*\" to patterns_allowed"
}
