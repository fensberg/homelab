# Every OpenTofu provider is a declared supplier, and every declared supplier
# is actually used.
#
# Providers are the most privileged deliveries this estate accepts: binaries
# downloaded at init time and executed locally, each handed a live credential -
# the hypervisor API token, the cluster PKI, the object storage keys. The
# hooks section of approved-suppliers.yml exists because seven repositories run
# on a developer's machine with a developer's privileges; this is the same
# shape at the estate's privilege, and it went undeclared until #230.
#
# Checked in both directions on purpose. One direction stops a provider being
# added to the HCL without anybody deciding to take delivery from that party;
# the other stops the list keeping an entry for a provider nobody uses, which
# is how an approval outlives the thing it approved.
package rules.supplier_providers

import data.lib
import rego.v1

list := [f.contents | some f in lib.at("scripts/approved-suppliers.yml")][0]

# A provider is found by what tofu init locked for a root or a module: the
# providers it was initialised with, by source, the registry's host left
# off as the approved list writes them. A source changed in the HCL and not
# in the lock does not initialise, so the plans refuse it; changed in both,
# it is here.
required contains [path, short] if {
	some [path, source] in lib.provider_locks
	parts := split(source, "/")
	short := concat("/", array.slice(parts, 1, count(parts)))
}

approved_providers contains p.source if {
	some p in object.get(list, "providers", [])
}

deny contains msg if {
	count(approved_providers) == 0
	msg := "approved-suppliers.yml declares no providers, so this rule proves nothing"
}

deny contains msg if {
	count(required) == 0
	msg := "no provider is locked by any root or module, so this rule proves nothing"
}

deny contains msg if {
	some source in {s | some [_, s] in required}
	not source in approved_providers
	first := min({p | some [p, s] in required; s == source})
	msg := $"{first} locks the provider \"{source}\", which is not an approved supplier. A provider runs with this estate's credentials, so taking delivery from a new party is a decision rather than a line of HCL. Declare it in scripts/approved-suppliers.yml with a reason, in its own pull request."
}

deny contains msg if {
	some source in approved_providers
	not source in {s | some [_, s] in required}
	msg := $"scripts/approved-suppliers.yml approves the provider \"{source}\" and no provider lock locks it. Remove the entry - an approval that outlives the thing it approved is how a supplier list stops describing what the estate actually takes."
}
