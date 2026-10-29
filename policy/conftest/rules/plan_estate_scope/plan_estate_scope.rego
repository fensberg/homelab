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

estate_types := {
	# Who may enroll a device, and what an enrolled device reaches.
	"cloudflare_zero_trust_access_policy",
	"cloudflare_zero_trust_access_application",
	"cloudflare_zero_trust_device_default_profile",
	# Each site's plot, created by the estate because only an account-wide
	# token can create it, and granted to the site as narrowed credentials.
	"cloudflare_r2_bucket",
	"cloudflare_account_token",
	# Each site's tunnel, what it serves and the routes to it, made by the
	# estate for the same reason.
	"cloudflare_zero_trust_tunnel_cloudflared",
	"cloudflare_zero_trust_tunnel_cloudflared_config",
	"cloudflare_zero_trust_tunnel_cloudflared_route",
	# The rule that answers an enrolled device, and nobody else, with where a
	# name a site serves is reached.
	"cloudflare_zero_trust_gateway_policy",
	# Whether the vendor carries what a device sends to a private name at all:
	# one setting for the account.
	"cloudflare_zero_trust_device_settings",
	# The watchman, which says when a site has gone quiet. The estate's because
	# it has to outlive whatever it watches: one in a site's state is destroyed
	# with the site.
	"cloudflare_workers_script",
	"cloudflare_workers_script_subdomain",
	"cloudflare_workers_cron_trigger",
	"cloudflare_workers_kv_namespace",
	# What each site rings the watchman with.
	"random_password",
	# What a workflow in a repository of the estate's may run, and what the
	# token it is handed may do: the estate is every repository under its
	# organisation, and a site has none.
	"github_actions_repository_permissions",
	"github_workflow_repository_permissions",
	# What may be pushed to a repository's branches and tags.
	"github_repository_ruleset",
	# What a member of the organisation may do for being one.
	"github_organization_settings",
	# Which repositories and workflows may use the estate's own runners.
	"github_actions_runner_group",
	# What a workflow reads that is the estate's to say: which site a pull
	# request's jobs are sent to.
	"github_actions_variable",
	# Where a job is handed the estate's own credential, and on which branch
	# it may be.
	"github_repository_environment",
	"github_repository_environment_deployment_policy",
}

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
	not r.type in estate_types
	msg := $"the estate's plan would make {r.address}, a {r.type}, which is not listed as the estate's. Everything in the estate's scope belongs to every site in the account. If this object does, add its type to estate_types in this rule; if it belongs to one site, it goes in that site's roots."
}

deny contains msg if {
	some ["site", r] in made
	r.type in estate_types
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
