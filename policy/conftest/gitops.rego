# Flux syncs a public repository, so it needs no credential at all.
#
# A GitRepository pointed at an https:// URL on a public repo clones
# anonymously. Giving it a secretRef anyway does not make the sync more
# secure - it makes an access token a live resource attribute in OpenTofu
# state, doing a job that an unauthenticated GET already does. That inverts
# this project's own rule about which secrets are worth generating: the ones
# that end up in state are the expensive ones, and this one buys nothing to
# pay for itself with.
#
# It is also the failure mode that is hardest to notice, because nothing
# breaks. The sync works with the credential and works without it, so the
# only signal that it was ever wrong is somebody reading gitops.tf and
# asking what the secret is for. That is what this rule is standing in for.
#
# Reintroducing it would take two steps - a token back in the config, and a
# secretRef back in gotk-sync.yaml - so both are checked. Either one alone is
# inert, which is exactly why neither would be caught by a run failing.
# about. Decoding into a struct rather than grepping means a secretRef
# indented into some other block cannot pass by not matching a regexp.
package rules.gitops

import data.lib
import rego.v1

sources contains f if {
	some f in lib.objects
	f.contents.kind == "GitRepository"
	not endswith(f.path, "gotk-components.yaml")
}

deny contains msg if {
	count(sources) == 0
	msg := "no GitRepository is declared, so this rule is reading the wrong files"
}

deny contains msg if {
	some f in sources
	f.contents.spec.secretRef
	msg := sprintf("%s gives the GitRepository a secretRef. This repository is public. An https:// clone of it needs no credential, so the secret this points at would be an access token living in OpenTofu state to authenticate a request that succeeds unauthenticated. If the repository has been made private, this rule is the wrong thing to change first - re-read the invariant above and decide deliberately, because the credential comes back with a state-exposure cost attached.", [f.path])
}

deny contains msg if {
	some f in sources
	not startswith(object.get(f.contents.spec, "url", ""), "https://")
	msg := sprintf("%s clones from %q, which is not an anonymous https:// URL. Anonymous sync is what makes the missing secretRef correct. An ssh remote needs a deploy key, which is the same credential this rule exists to keep out of state.", [f.path, object.get(f.contents.spec, "url", "")])
}

# And the config template, which is where the token would have to be
# declared before OpenTofu could reference it at all. Read as structure: the
# obvious string to search for is "source_control.token", and it would never
# have matched - the key is nested JSON, and the vault path it holds spells
# the item "source-control" with a hyphen. So any field under source_control
# whose name suggests a credential is refused, whatever the reference behind
# it looks like.

template := [f | some f in lib.at("config/management.tpl.json")]

deny contains msg if {
	not template[0].contents.source_control
	msg := "the config template no longer declares source_control - this rule is checking the wrong file"
}

deny contains msg if {
	some field in ["token", "password", "pat", "ssh_key", "deploy_key"]
	field in object.keys(template[0].contents.source_control)
	msg := sprintf("config/management.tpl.json declares source_control.%s. Declaring it renders it into config/management.rendered.json on every run, whether or not anything reads it - one more secret on disk for a clone that needs none.", [field])
}

# The break-glass identity is not in the config template. The rendered config
# is written to disk by the Render phase and read by every later phase. The
# recipient belongs there - it is public and the Backup phase needs it. The
# identity does not: it is only ever needed by a human performing a restore
# (policy/semgrep/breakglass-opentofu.yml keeps it out of OpenTofu too).

template_says(word) if {
	walk(template[0].contents, [path, v])
	some s in array.concat([v | is_string(v)], [k | some k in path; is_string(k)])
	contains(s, word)
}

deny contains msg if {
	some word in ["state_backup/identity", "state_backup.identity", "backup_identity"]
	template_says(word)
	msg := sprintf("config/management.tpl.json references %q. Adding it there would render the private half into config/management.rendered.json on every run, next to every other secret. The recipient belongs in the template because encryption needs it; the identity is only ever needed by a human performing a restore.", [word])
}

deny contains msg if {
	template[0]
	not template_says("state_backup/recipient")
	msg := "the config template no longer references state_backup/recipient - the rule keeping the identity out of it is checking the wrong file"
}
