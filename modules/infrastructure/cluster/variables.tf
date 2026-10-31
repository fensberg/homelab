variable "site" {
  type        = string
  default     = "site0"
  description = <<-EOT
    Which key in the config's sites map to deploy, e.g. "site0". Selection
    only - the site's identity comes from the octet it declares. Set by the
    start button via TF_VAR_site.
  EOT
  # Checked here rather than as a precondition on terraform_data.invariants,
  # where it used to live and could never actually fire: local.site indexes
  # sites[var.site] directly, so a mistyped -site failed on a raw "Invalid
  # index" against this file's own line 25 long before any precondition was
  # evaluated. A variable validation runs before locals are evaluated at all,
  # and - unlike a resource precondition - cannot be targeted away by the
  # -target'd applies the Compute phase issues.
  #
  # Referencing another variable from a validation needs OpenTofu >= 1.9,
  # which is why versions.tf's required_version is no longer >= 1.6.0.
  validation {
    condition     = contains(keys(jsondecode(file(var.config_path)).sites), var.site)
    error_message = "Unknown site '${var.site}'. The config at ${var.config_path} defines: ${join(", ", sort(keys(jsondecode(file(var.config_path)).sites)))}."
  }
}

variable "offline" {
  type    = bool
  default = false

  description = <<-EOT
    Whether this plan must reach nothing: set only by the Record phase, which
    plans against the as-built record with stand-in credentials and no
    network. It turns off the cluster health read, the one data source a plan
    with -refresh=false still performs (#554).
  EOT
}

variable "overlay_key_wanted" {
  type    = bool
  default = false

  description = <<-EOT
    Whether the hypervisor's tagged auth key should exist right now.

    False by default, which means the key does not exist unless something is
    about to use it. Only the Overlay phase sets it true, and only the
    hypervisor playbook consumes what that mints - minutes later, once.

    This is what stops the key being reconciled by every other apply. The
    Cluster phase ends with an untargeted apply, so with a plain unconditional
    resource the key was in the graph on every run: the one-hour expiry made
    the provider see it as gone, and the apply replaced it. A converge does not
    run the hypervisor phase, so every converge minted a route-approving,
    pre-authorized, reusable key that nothing would ever read, and dropped it
    (#138).

    Conditional rather than long-lived on purpose. Lengthening the expiry would
    also stop the churn, and would do it by keeping a valid subnet-router
    credential alive for months - trading away the property that makes a leak
    survivable, which is the wrong direction for this estate. With `count` the
    key exists across the window that needs it and the next untargeted apply
    revokes it, which is better than letting it expire: revoked is a state
    Tailscale enforces, expired is one it merely records.
  EOT
}

variable "config_path" {
  type        = string
  default     = "../../config/management.rendered.json"
  description = <<-EOT
    Path to the rendered config JSON. Overridden only by tofu test, to point
    at a fixture instead of the real rendered config - a real run, task
    validate's placeholder render, and CI never set this, so the default is
    the only path any of them ever see.
  EOT
}

locals {
  config = jsondecode(file(var.config_path))

  site = local.config.sites[var.site]

  # Everything nameable uses the site's own name, so Proxmox, Talos, kubeconfig
  # and the Tailscale console all say the site's own name rather than a
  # positional key
  # nobody recognises. The name is a vault reference, so it never reaches git.
  #
  # Sanitised because these become Proxmox VM names and a Talos cluster name,
  # which are DNS-shaped: a label like "North Street Office" has to collapse
  # to "north-street-office". Falls back to the map key if the name is blank.
  site_name = local.net.slug

  hypervisors = [for k in sort(keys(local.site.hypervisor.nodes)) : local.site.hypervisor.nodes[k]]

  # Each hypervisor's datastores and hostname, by its key in the config
  # (node0), which is what a machine's placement names. They are facts about
  # the host, so they come from its config entry.
  #
  # BY THE KEY, NEVER A VALUE FROM THE VAULT. Whatever a resource is keyed by
  # is part of its address, and every tool that touches a resource prints its
  # address: plans, apply output, the contractor's progress lines, CI logs. The
  # hostname is a vault value, so it is looked up here, where it is an
  # attribute, and is never a for_each key. Nothing checks this one field;
  # every plan is checked for a key holding any vault value at all, and the
  # as-built record refuses an estate that has one (#585,
  # asbuilt.KeyedByAVaultValue).
  datastores = { for k, h in local.site.hypervisor.nodes : k => try(h.datastores, {}) }
  hostnames  = { for k, h in local.site.hypervisor.nodes : k => h.hostname }

  # Per-site because two sites are two estates: separate hypervisors, separate
  # tailnets when the engagement calls for it, separate buckets, and separate
  # state databases. Sharing any of them means compromising one site reaches
  # the others.
  overlay_network = local.site.overlay_network
  object_storage  = local.site.object_storage

  node_count = local.site.control_plane_count

  # --- addressing ----------------------------------------------------------
  # The octet is declared, not computed. Reading the config tells you the
  # site's network without doing arithmetic, retiring a site means leaving a
  # gap rather than renumbering, and reordering sites[] no longer silently
  # repoints an estate at someone else's network.
  #
  # The cost is that collisions become expressible, so registry.tf asserts
  # uniqueness across every site rather than relying on the schema.
  octet     = local.site.octet
  site_cidr = local.net.site_cidr

  # The Talos image variant the Factory is asked for.
  #
  # `nocloud` is the platform - it makes Talos read its configuration from a
  # cloud-init datasource, which is how compute.tf hands each node its address -
  # and `amd64` is the architecture. Both were correct and undeclared, spelled
  # into two image URLs where nothing said they were a choice (#360).
  #
  # Defensible on x86 Proxmox and not universal: a fork on arm64 hardware, or
  # one handing configuration to nodes some other way, changes this line. That
  # it is one line is the point.
  image_variant = "nocloud-amd64"

  # Where every machine in the estate resolves names.
  #
  # DECLARED ONCE because it was declared four times - twice in compute.tf's
  # cloud-init and twice in talos.tf's ResolverConfig - which is the same
  # duplication the one-place rule already forbids for versions, and it drifts
  # the same way. Four copies of a value is not a value, it is four chances for
  # three of them to be right (#360).
  #
  # AND BECAUSE IT IS A DECISION NOBODY WROTE DOWN. Two things are being chosen
  # here and neither was ever stated. Every name this estate looks up is
  # resolved by one third party, so the estate's name resolution has that
  # party's availability as a dependency; and the queries themselves - which is
  # every host the cluster ever contacts - are visible to them. Both are
  # defensible and neither is obvious, which is exactly the class of choice that
  # should not live as a literal repeated in two files.
  #
  # THE PATH FOR A FORK, which is the other half of #360: change the list in
  # config/facts/dns-resolvers.json. A network with internal resolvers,
  # split-horizon DNS, or a policy against public upstreams needs nothing
  # else - every machine the estate builds reads from here.
  # policy/conftest/rules/dns_resolvers refuses a restatement.
  #
  # Not moved into the vault template: a resolver address is not a secret, and
  # putting it there would make a fork edit a vault item to change a routing
  # decision that belongs in code where it can be reviewed.
  dns_resolvers = jsondecode(file("${path.module}/../../../config/facts/dns-resolvers.json")).servers

  # The two largest address commitments in the estate, the site's own.
  #
  # They were Kubernetes' defaults once (#240), then declared as those defaults
  # - 10.244.0.0/16 and 10.96.0.0/12 at every site - which was harmless until
  # two sites route to each other, and then impossible to route. The address
  # plan now allocates each site its own (pods 10.<100+octet>.0.0/16, services
  # a /22 of 10.196.0.0/14), site0 included, and checks that none can meet a
  # site network or each other (cluster_network_test.go asks it for every
  # octet a site may have).
  #
  # WHY THIS IS NOT AN EDIT LATER. clusterNetwork is fixed at cluster creation.
  # A change here is a rebuild, not an apply: the site is rebuilt from the
  # branch that changes it, before it merges.
  #
  # WHAT IT CAPS. A /16 handing out a /24 per node is 256 nodes, and a /22 is
  # 1024 Services, of which Kubernetes keeps the bottom 64 for addresses chosen
  # by hand, which is where an application's declared routes go.
  pod_cidr     = local.net.pod_cidr
  service_cidr = local.net.service_cidr

  # Every address, VM id and machine name below is the address plan's
  # (modules/infrastructure/address-plan, called in address-plan.tf), which
  # computes them once for every site; these are views of its answer for this
  # site. The contractor and the test harness ask the same module, so nothing
  # restates the scheme (docs/epochs/02-abstraction.md).
  net          = module.address_plan.sites[var.site]
  node_cidr    = local.net.node_cidr
  node_gateway = local.net.node_gateway

  # Read from the config for registry.tf's checks. The ceiling keeps the
  # zones' subnets inside the band the address plan reserves for them.
  worker_count   = try(local.site.worker_count, 0)
  dmz_zones_in   = try(local.site.dmz_zones, {})
  dmz_zone_names = sort(keys(local.dmz_zones_in))
  dmz_max_zones  = 10

  control_plane = local.net.control_planes
  workers       = local.net.workers
  dmz_zones     = local.net.dmz_zones
  dmz           = local.net.dmz

  dmz_keys  = sort(keys(local.dmz))
  dmz_ips   = [for k in local.dmz_keys : local.dmz[k].ip]
  dmz_names = [for k in local.dmz_keys : local.dmz[k].name]

  # Only hypervisors actually hosting an untrusted machine pull the second
  # image. A ~4.5GB decompressed disk image on a node with nothing to run it is
  # a cost with no purpose, and the set is empty when no zone is declared.
  dmz_hypervisors = distinct([for k in local.dmz_keys : local.dmz[k].hypervisor])

  # Ordered views, for the places that genuinely need a list: the first node is
  # the cluster endpoint and the NodePort host, and the health data source takes
  # every address. Sorted by key, which for three-digit octets is numeric order.
  cp_keys  = sort(keys(local.control_plane))
  node_ips = [for k in local.cp_keys : local.control_plane[k].ip]

  # Kept separate from node_ips rather than appended to it. node_ips is what
  # the etcd health check and the cluster endpoint are built from, and a worker
  # is neither an etcd member nor a candidate endpoint - folding the two lists
  # together would put a worker's address somewhere it can only be wrong.
  worker_keys = sort(keys(local.workers))
  worker_ips  = [for k in local.worker_keys : local.workers[k].ip]

  # --- placement -----------------------------------------------------------
  # Control-plane VMs are dealt round-robin across whatever hypervisors the
  # site has. One hypervisor puts all of them on it; three put one on each,
  # which is what makes the cluster survive losing a box. Appending a node to
  # sites[].hypervisor.nodes is all it takes here - but a multi-node Proxmox
  # cluster also needs a vxlan or evpn SDN zone, see docs/epochs/01-ignition.md.
  vm_placement       = [for k in local.cp_keys : local.control_plane[k].hypervisor]
  worker_placement   = [for k in local.worker_keys : local.workers[k].hypervisor]
  all_vm_hypervisors = distinct(concat(local.vm_placement, local.worker_placement))

  # --- identity ------------------------------------------------------------
  # Everything nameable carries the site, so two sites are distinguishable at
  # a glance in Proxmox, in Talos and in kubeconfig.
  # site.name is a vault reference, so the human label for a site never
  # reaches git while still appearing in the cluster name.
  cluster_name = trim(lower(replace(
    "${local.config.organization.name}-${local.site_name}",
  "/[^A-Za-z0-9]+/", "-")), "-")
  # One number per node, used three ways.
  #
  # It used to be three: the IP was host octet 100+i, the name was i+1, and the
  # VM id was octet*100+i - so one machine was .100, cp-01 and 1000 at the same
  # time. Nothing was wrong with any of them alone, and together they meant
  # every cross-reference during an incident needed arithmetic.
  #
  # Now the host octet is the number. The name carries it, the id ends in it,
  # and it is the last octet of the address:
  #
  #     10.10.10.100   <site>-cp-100   vm 10100
  vm_names     = [for k in local.cp_keys : local.control_plane[k].name]
  worker_names = [for k in local.worker_keys : local.workers[k].name]

  # --- platform ------------------------------------------------------------
  # What the machines run: the Talos and Kubernetes versions and the image
  # schematics. Declared once in modules/infrastructure/machine-image, which
  # the platform module reads too, so that what the machines are built from
  # and what they are rolled to are the one fact.
  talos_version      = module.machine_image.talos_version
  kubernetes_version = module.machine_image.kubernetes_version
  schematic_id       = module.machine_image.schematic_id
  dmz_schematic_id   = module.machine_image.dmz_schematic_id

}

output "site_network" {
  description = "Everything derived from the site index."
  value = {
    site         = local.site_name
    site_cidr    = local.site_cidr
    node_cidr    = local.node_cidr
    node_gateway = local.node_gateway
    node_ips     = local.node_ips
    cluster_name = local.cluster_name
    vm_names     = local.vm_names
    hypervisors  = sort(keys(local.site.hypervisor.nodes))
    vm_placement = local.vm_placement

    # The workers belong here for the same reason the control plane does: this
    # output is the estate's own account of what it built, and one that stops
    # at the control plane describes an estate that no longer exists. Their
    # absence was caught by tflint noticing worker_names had no consumer, which
    # was the honest signal - a derived value nothing reads is either dead or
    # missing from somewhere, and here it was the second.
    worker_ips       = local.worker_ips
    worker_names     = local.worker_names
    worker_placement = local.worker_placement

    # The untrusted zone is reported alongside the rest for the same reason the
    # workers are: a tier nothing consumes is a tier tflint cannot see, and this
    # one has its own subnet, so leaving it out would make the estate's own
    # description of its addressing incomplete in the place it matters most.
    dmz_zones = local.dmz_zones
    dmz_ips   = local.dmz_ips
    dmz_names = local.dmz_names
  }
}
