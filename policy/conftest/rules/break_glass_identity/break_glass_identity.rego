# The break-glass identity is not in the config template. The rendered config
# is written to disk by the Render phase and read by every later phase. The
# recipient belongs there - it is public and the Backup phase needs it. The
# identity does not: it is only ever needed by a human performing a restore
# (policy/semgrep/breakglass-opentofu.yml keeps it out of OpenTofu too).
package rules.break_glass_identity

import data.lib
import rego.v1

template := [f | some f in lib.at("config/management.tpl.json")]

template_says(word) if {
	walk(template[0].contents, [path, v])
	some s in array.concat([v | is_string(v)], [k | some k in path; is_string(k)])
	contains(s, word)
}

deny contains msg if {
	count(template) != 1
	msg := $"found no config template at config/management.tpl.json ({count(template)} read), so whether it holds the break-glass identity cannot be read"
}

deny contains msg if {
	some word in ["state_backup/identity", "state_backup.identity", "backup_identity"]
	template_says(word)
	msg := $"config/management.tpl.json references \"{word}\". Adding it there would render the private half into config/management.rendered.json on every run, next to every other secret. The recipient belongs in the template because encryption needs it; the identity is only ever needed by a human performing a restore."
}

deny contains msg if {
	template[0]
	not template_says("state_backup/recipient")
	msg := "the config template no longer references state_backup/recipient - the rule keeping the identity out of it is checking the wrong file"
}
