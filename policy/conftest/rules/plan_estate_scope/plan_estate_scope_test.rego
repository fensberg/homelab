package rules.plan_estate_scope_test

import data.rules.plan_estate_scope
import rego.v1

resource(address, type) := {"address": address, "mode": "managed", "type": type, "values": {}}

plan(scope, resources) := {"path": $"/tmp/plans/{scope}/{scope}.plan.json", "contents": {"planned_values": {"root_module": {"resources": resources}}}}

lock(path, sources) := {"path": path, "contents": {"provider": {s: [{"version": "1.0.0", "hashes": ["h1:x"]}] | some s in sources}}}

estate := plan("estate", [resource("github_repository_ruleset.checks", "github_repository_ruleset")])

site := plan("site", [resource("proxmox_virtual_environment_vm.cp", "proxmox_virtual_environment_vm")])

site_lock := lock("management/cluster/.terraform.lock.hcl", ["registry.opentofu.org/bpg/proxmox"])

good := [estate, site, site_lock]

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_each_scope_keeping_to_its_own_passes if {
	count(plan_estate_scope.deny) == 0 with input as good
}

test_an_estate_object_of_an_unlisted_type_is_refused if {
	unlisted := plan("estate", [resource("github_repository_file.planted", "github_repository_file")])
	denies(plan_estate_scope.deny, "which is not listed as the estate's") with input as [unlisted, site, site_lock]
}

test_a_data_source_in_the_estate_is_not_an_object_it_makes if {
	reads := plan("estate", [resource("github_repository_ruleset.checks", "github_repository_ruleset"), object.union(resource("data.github_user.me", "github_user"), {"mode": "data"})])
	count(plan_estate_scope.deny) == 0 with input as [reads, site, site_lock]
}

test_an_estate_object_in_a_sites_plan_is_refused if {
	held := plan("site", [resource("random_password.ring", "random_password")])
	denies(plan_estate_scope.deny, "which is the estate's") with input as [estate, held, site_lock]
}

test_a_sites_lock_with_the_accounts_provider_is_refused if {
	widened := lock("management/platform/.terraform.lock.hcl", ["registry.opentofu.org/hashicorp/kubernetes", "registry.opentofu.org/cloudflare/cloudflare"])
	denies(plan_estate_scope.deny, "locks registry.opentofu.org/cloudflare/cloudflare, the account's") with input as [estate, site, site_lock, widened]
}

test_the_estates_own_lock_may_hold_the_accounts_provider if {
	estates := lock("management/estate/.terraform.lock.hcl", ["registry.opentofu.org/cloudflare/cloudflare", "registry.opentofu.org/integrations/github"])
	count(plan_estate_scope.deny) == 0 with input as array.concat(good, [estates])
}

test_nothing_to_read_is_refused if {
	denies(plan_estate_scope.deny, "no plan of the estate's scope makes anything") with input as []
	denies(plan_estate_scope.deny, "no plan of a site's scope makes anything") with input as []
	denies(plan_estate_scope.deny, "no provider lock locks what builds the machines") with input as []
}
