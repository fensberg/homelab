# The estate's objects are in the estate's scope, and nowhere else.
#
# Things are built in three scopes. The estate - the Cloudflare account and its
# Zero Trust organisation, and the source control organisation - belongs to
# every site; a site belongs to every node in it; and an operation at one scope
# must be unable to harm anything wider. The enrollment application once lived
# in a site's state, adopted there, and a site demolish destroyed it for the
# whole account; the next site build then failed looking for it (#531).
#
# The boundary is state, not care: the estate's root has its own state, so a
# site's plan has no handle on an estate object and a site's destroy cannot
# reach one. It holds in both directions, on what OpenTofu would make and on
# what it was initialised with:
#
#   - every object the estate's plan would make is a type listed below as the
#     estate's. A new estate object is a decision about scope, and this is
#     where it is written down;
#   - no site's plan would make one; and no root or module of a site's - one
#     that locks what builds the machines or what talks to the cluster - is
#     initialised with a provider of the account's, so it could not declare
#     one without its lock saying so.
#
# A plan's scope is the directory the rule harness writes it under
# (lib.plan_scope); a site's lock is found by the providers it locks.
package rules.plan_estate_scope

import data.lib
import rego.v1

# Each estate type, and how a change to it is proved once it has merged.
#
# A change to the estate takes effect when the estate converges after the
# merge, never on the pull request, so what proves it has to run then
# (#885). Every type is one of two:
#
#   - "read back": the converge plans again straight after it applies and
#     fails when anything is left to change, so the estate is what its
#     declarations say (scripts/lawyer, readBack);
#   - "probe": declared as applied is not enough, because what the setting
#     refuses only shows when something asks it - the allowed-actions
#     setting, proved by a job referencing every allowed action
#     (policy/conftest/rules/allowed_actions_probe).
#
# A new type is a decision about scope and about its proof, and both are
# written here.
estate_types := {
	# Who may enroll a device, and what an enrolled device reaches.
	"cloudflare_zero_trust_access_policy": "read back",
	"cloudflare_zero_trust_access_application": "read back",
	"cloudflare_zero_trust_device_default_profile": "read back",
	# Each site's plot, created by the estate because only an account-wide
	# token can create it, and granted to the site as narrowed credentials.
	"cloudflare_r2_bucket": "read back",
	"cloudflare_account_token": "read back",
	# Each site's tunnel, what it serves and the routes to it, made by the
	# estate for the same reason.
	"cloudflare_zero_trust_tunnel_cloudflared": "read back",
	"cloudflare_zero_trust_tunnel_cloudflared_config": "read back",
	"cloudflare_zero_trust_tunnel_cloudflared_route": "read back",
	# The rule that answers an enrolled device, and nobody else, with where a
	# name a site serves is reached.
	"cloudflare_zero_trust_gateway_policy": "read back",
	# Whether the vendor carries what a device sends to a private name at all:
	# one setting for the account.
	"cloudflare_zero_trust_device_settings": "read back",
	# The watchman, which says when a site has gone quiet. The estate's because
	# it has to outlive whatever it watches: one in a site's state is destroyed
	# with the site.
	"cloudflare_workers_script": "read back",
	"cloudflare_workers_script_subdomain": "read back",
	"cloudflare_workers_cron_trigger": "read back",
	"cloudflare_workers_kv_namespace": "read back",
	# What each site rings the watchman with.
	"random_password": "read back",
	# What a workflow in a repository of the estate's may run, and what the
	# token it is handed may do: the estate is every repository under its
	# organisation, and a site has none.
	"github_actions_repository_permissions": "probe",
	"github_workflow_repository_permissions": "read back",
	# What may be pushed to a repository's branches and tags.
	"github_repository_ruleset": "read back",
	# What a member of the organisation may do for being one.
	"github_organization_settings": "read back",
	# Which repositories and workflows may use the estate's own runners.
	"github_actions_runner_group": "read back",
	# What a workflow reads that is the estate's to say: which site a pull
	# request's jobs are sent to.
	"github_actions_variable": "read back",
	# Where a job is handed the estate's own credential, and on which branch
	# it may be.
	"github_repository_environment": "read back",
	"github_repository_environment_deployment_policy": "read back",
}

# The two proofs a change to the estate has after merge.
proofs := {"read back", "probe"}

# What makes nothing outside OpenTofu's own state, so belongs to no scope: a
# value kept between runs so a change to it can be acted on. Either scope may
# hold one, and it is not the estate's - which would refuse it in a site.
state_only := {"terraform_data"}

# The providers of the account and of source control: whoever is initialised
# with one can declare the estate's objects.
account_wide := {
	"registry.opentofu.org/cloudflare/cloudflare",
	"registry.opentofu.org/integrations/github",
}

# What a site's roots and modules are initialised with: what builds the
# machines, and what talks to the cluster's API.
a_sites := {
	"registry.opentofu.org/bpg/proxmox",
	"registry.opentofu.org/hashicorp/kubernetes",
}

made contains [scope, r] if {
	some [path, r] in lib.plan
	r.mode == "managed"
	scope := lib.plan_scope(path)
}

sites_locks contains path if {
	some [path, source] in lib.provider_locks
	source in a_sites
}

deny contains msg if {
	some ["estate", r] in made
	not estate_types[r.type]
	not r.type in state_only
	msg := $"the estate's plan would make {r.address}, a {r.type}, which is not listed as the estate's. Everything in the estate's scope belongs to every site in the account. If this object does, add its type to estate_types in this rule, with how a change to it is proved after merge; if it belongs to one site, it goes in that site's roots."
}

deny contains msg if {
	some ["site", r] in made
	estate_types[r.type]
	msg := $"a site's plan would make {r.address}, a {r.type}, which is the estate's. An estate object in a site's state is destroyed by that site's demolish, for every site in the account - which is how the enrollment application was lost (#531). Declare it in the estate's root; a site that needs it reads it with a data source."
}

deny contains msg if {
	some [path, source] in lib.provider_locks
	path in sites_locks
	source in account_wide
	msg := $"{path} is a site's - it locks what builds the machines or talks to the cluster - and it locks {source}, the account's. A site initialised with it can declare the estate's objects into its own state, where its demolish destroys them for every site (#531)."
}

deny contains msg if {
	count({r | some ["estate", r] in made}) == 0
	msg := "no plan of the estate's scope makes anything, so whether the estate keeps to its own objects was not checked"
}

deny contains msg if {
	count({r | some ["site", r] in made}) == 0
	msg := "no plan of a site's scope makes anything, so whether a site holds the estate's objects was not checked"
}

deny contains msg if {
	count(sites_locks) == 0
	msg := "no provider lock locks what builds the machines or talks to the cluster, so no site's lock was checked"
}

deny contains msg if {
	some type, proof in estate_types
	not proof in proofs
	msg := $"estate_types says a change to {type} is proved by {proof}, which is no proof the estate has. After merge the estate is read back, or probed: say which."
}
