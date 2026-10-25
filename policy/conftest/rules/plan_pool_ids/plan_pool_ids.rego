# A pool's id carries the site, because pools are datacenter-scoped.
#
# Two sites sharing a Proxmox cluster would collide on a bare
# `control-plane`, and the collision does not error - Proxmox adopts the
# other estate's machines into this estate's pool, silently. The config
# supports several sites by design, so the collision is expressible rather
# than theoretical.
#
# ON THE PLAN'S CONFIGURATION. The site's name is a value the vault holds,
# so in a plan made from stand-ins it reads as the stand-in, and a rule may
# print no value. What the id is made from says it: every pool's id refers
# to the site's name.
package rules.plan_pool_ids

import data.lib
import rego.v1

pools contains [address, r] if {
	some [_, address, r] in lib.plan_configuration
	r.type == "proxmox_virtual_environment_pool"
}

deny contains msg if {
	count(pools) == 0
	msg := "no proxmox_virtual_environment_pool is in any plan's configuration, so this checked nothing"
}

deny contains msg if {
	some [address, r] in pools
	not "local.site_name" in object.get(object.get(r.expressions, "pool_id", {}), "references", [])
	msg := sprintf("%s has a pool id that does not include the site. Pools are datacenter-scoped, so an unprefixed id collides the moment two sites share a Proxmox cluster - and Proxmox answers a collision by adopting the other estate's machines into this pool rather than by failing.", [address])
}
