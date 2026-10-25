# Every App key a workflow is handed is proved by the patrol (#527).
#
# A key is used only when there is something to do with it, so one that has
# stopped working is found on the day it is needed. The patrol's keys job
# mints a token with each and throws it away - which holds only for the keys
# it is handed. And it watches; it changes nothing.
package rules.app_keys

import data.lib
import rego.v1

patrol := ".github/workflows/security-patrol.yml"

key_pattern := `secrets\.([A-Z][A-Z0-9_]*_PRIVATE_KEY)\b`

keys_in(value) := {m[1] | some m in regex.find_all_string_submatch_n(key_pattern, value, -1)}

proved contains key if {
	some step in lib.workflows[patrol].jobs.keys.steps
	some part in ["env", "with"]
	some v in object.get(step, part, {})
	is_string(v)
	some key in keys_in(v)
}

held contains [key, file] if {
	some file, doc in lib.workflows
	walk(doc, [_, v])
	is_string(v)
	some key in keys_in(v)
}

deny contains msg if {
	not lib.workflows[patrol].jobs.keys
	msg := $"{patrol} has no \"keys\" job, so no App key is proved to still work"
}

deny contains msg if {
	lib.workflows[patrol].jobs.keys
	count(proved) == 0
	msg := $"the keys job of {patrol} is handed no App key, so it proves nothing"
}

deny contains msg if {
	some key in {k | some [k, _] in held}
	not key in proved
	where := concat(", ", sort({basename(f) | some [k, f] in held; k == key}))
	msg := $"{key} is handed to {where} and the patrol's keys job does not prove it still mints a token, so it can stop working unseen until the day it is needed. Add a step there that signs in with it the way its own consumer does."
}

deny contains msg if {
	some name, job in lib.workflows[patrol].jobs
	some scope, level in object.get(job, "permissions", {})
	level == "write"
	msg := $"the patrol's {name} job can write {scope}. It watches, and proves keys by minting tokens it throws away; it changes nothing."
}

basename(path) := parts[count(parts) - 1] if parts := split(path, "/")
