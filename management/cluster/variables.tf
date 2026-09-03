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
  site_name = (
    trim(lower(replace(try(local.site.name, ""), "/[^A-Za-z0-9]+/", "-")), "-") != ""
    ? trim(lower(replace(local.site.name, "/[^A-Za-z0-9]+/", "-")), "-")
    : var.site
  )

  # nodes is a map, so iterate it in sorted key order: node0, node1, node2.
  # HCL orders map iteration lexicographically, which keeps VM placement
  # deterministic between runs and matches what the start button does.
  hypervisors = [for k in sort(keys(local.site.hypervisor.nodes)) : local.site.hypervisor.nodes[k]]

  # Per-site because two sites are two estates: separate hypervisors, separate
  # tailnets when the engagement calls for it, separate buckets, and separate
  # state databases. Sharing any of them means compromising one site reaches
  # the others.
  overlay_network = local.site.overlay_network
  object_storage  = local.site.object_storage

  # Top level, not inside the site: an account id and an admin token describe
  # the vendor account rather than one estate. See the epoch record, "The
  # object storage account plane is not the site plane".
  object_storage_account = local.config.object_storage
  site_database          = local.site.database

  # --- CI runners ----------------------------------------------------------
  # Fleet plane, like the object storage account: one GitHub App serves the
  # estate, and a runner is a site-level deployment of an estate-level
  # identity. See runner.tf for why the App is scoped the way it is.
  runner = local.config.source_control.foreman_bot

  # Only what OpenTofu itself creates. The scale set's own name, its runner
  # group and its ceiling are declared in the manifest that uses them, because
  # nothing here reads them - a local kept alive purely so a test could compare
  # against it is dead code with a test-shaped excuse, and tflint was right to
  # say so.
  runner_system_namespace = "arc-systems"
  runners_namespace       = "arc-runners"
  runner_secret_name      = "github-app-credentials"

  # The scale set is registered against the organization, not the repository,
  # because that is the scope the App was granted. Derived from the repository
  # URL rather than declared again, so the two cannot disagree:
  # https://host/org/repo -> https://host/org.
  runner_github_config_url = join("/", slice(split("/", local.config.source_control.repo_url), 0, 4))
  node_count               = local.site.control_plane_count

  # --- addressing ----------------------------------------------------------
  # The octet is declared, not computed. Reading the config tells you the
  # site's network without doing arithmetic, retiring a site means leaving a
  # gap rather than renumbering, and reordering sites[] no longer silently
  # repoints an estate at someone else's network.
  #
  # The cost is that collisions become expressible, so registry.tf asserts
  # uniqueness across every site rather than relying on the schema.
  octet     = local.site.octet
  site_cidr = "10.${local.octet}.0.0/16"

  # Talos control plane. Infrastructure sits in .0.0/24 and a load-balancer
  # pool is reserved at .20.0/24 for epoch 02.
  node_cidr    = "10.${local.octet}.10.0/24"
  node_gateway = cidrhost(local.node_cidr, 1)
  # The one number every other identifier is derived from.
  host_octets = [for i in range(local.node_count) : 100 + i]

  # The control plane, keyed by host octet rather than by position.
  #
  # for_each, not count. With count the key is a position, so removing a node
  # renumbers every node after it and OpenTofu replaces all of them - a running
  # etcd member destroyed for being third instead of fourth, with nothing
  # calling `talosctl etcd remove-member` first. Keyed by the host octet,
  # identity is the machine rather than its place in a list: adding "105" is
  # one create, removing "102" is one destroy, and nothing else moves.
  #
  # That is the difference between a control plane whose nodes can be replaced
  # one at a time and one that can only be rebuilt.
  control_plane = {
    for i, h in local.host_octets : tostring(h) => {
      host_octet = h
      ip         = cidrhost(local.node_cidr, h)
      name       = format("%s-cp-%d", local.site_name, h)

      # Banded by octet so two sites can share a Proxmox cluster without
      # colliding, and ending in the host octet so the id reads back as the
      # address: octet 10 uses 10100-10199, octet 11 uses 11100-11199. The
      # octet is asserted 1-95, so the widest band is 95100-95199 - well
      # inside Proxmox's range.
      vm_id = local.octet * 1000 + h

      # Still a re-deal when the hypervisor count changes - see
      # docs/epochs/02-abstraction.md, "Adding a hypervisor currently re-deals
      # the control plane". Keying by host octet fixes identity churn, not
      # placement churn; the placement fix needs the assignment to be recorded
      # rather than recomputed, which is its own change.
      #
      # Guarded rather than indexed directly: a site with no hypervisors has an
      # empty list, and `i % 0` is an error that aborts evaluation before
      # registry.tf's precondition can say "site has no hypervisor nodes" in
      # words. The corpus caught exactly that. The empty string never reaches a
      # resource, because that precondition stops the plan first.
      hypervisor = length(local.hypervisors) > 0 ? local.hypervisors[i % length(local.hypervisors)].hostname : ""
    }
  }

  # Ordered views, for the places that genuinely need a list: the first node is
  # the cluster endpoint and the NodePort host, and the health data source takes
  # every address. Sorted by key, which for three-digit octets is numeric order.
  cp_keys  = sort(keys(local.control_plane))
  node_ips = [for k in local.cp_keys : local.control_plane[k].ip]

  # --- placement -----------------------------------------------------------
  # Control-plane VMs are dealt round-robin across whatever hypervisors the
  # site has. One hypervisor puts all of them on it; three put one on each,
  # which is what makes the cluster survive losing a box. Appending a node to
  # sites[].hypervisor.nodes is all it takes here - but a multi-node Proxmox
  # cluster also needs a vxlan or evpn SDN zone, see docs/epochs/01-ignition.md.
  vm_placement = [for k in local.cp_keys : local.control_plane[k].hypervisor]

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
  vm_names = [for k in local.cp_keys : local.control_plane[k].name]

  # --- platform ------------------------------------------------------------
  # renovate: datasource=github-releases depName=siderolabs/talos
  #
  # This line, not schematic_id, is what selects extension versions. The image
  # URL in compute.tf is factory.talos.dev/image/<schematic>/<talos_version>/…,
  # and the Factory resolves each extension to the build matching that Talos
  # release. The schematic pins *which* extensions; this pins *their versions*.
  #
  # v1.13.8 resolved siderolabs/tailscale to 1.98.9, which the Tailscale console
  # flags as carrying a known vulnerability on every device in the estate,
  # hypervisor included (#100). v1.13.9 resolves it to 1.102.2. The schematic id
  # is unchanged by this - it did not need re-minting, which the issue assumed.
  #
  # Bumped here rather than later because #97: a Talos image change cannot reach
  # a running estate, so a rebuild is the only delivery mechanism there is. This
  # branch is merged between a demolish and a break-ground, which is the only
  # window in which this costs nothing.
  talos_version = "v1.13.9"

  # renovate: datasource=github-releases depName=kubernetes/kubernetes
  #
  # Was inline in talos.tf as a bare "1.31.1" with no comment, no annotation
  # and no entry in versions.env - so nothing watched it and nothing could
  # have told you it had gone stale. It had: Talos v1.13.9 defaults to
  # Kubernetes 1.36.3 and supports six minors back, which put 1.31 at the
  # oldest edge of the platform carrying it and outside upstream Kubernetes'
  # own patch window entirely.
  #
  # Moved here for the same reason talos_version is here rather than inline:
  # a version this estate runs on should sit where versions are read, beside
  # the one it has to stay compatible with. The two move together - Talos
  # decides which Kubernetes versions are installable at all.
  #
  # Bumped in the same window and on the same reasoning as the Talos bump
  # above: #97 means an image change cannot reach a running estate, and
  # changing the control plane's Kubernetes version on a live cluster is an
  # upgrade rather than an apply - epoch 05's work, not epoch 01's. This
  # branch is merged between a demolish and a break-ground, which is the only
  # window in which it costs nothing. Ignite on 1.31 and the estate carries an
  # out-of-support control plane until epoch 05 exists to move it.
  #
  # tests/go/repo/versions_test.go asserts kubectl stays within one minor.
  kubernetes_version = "1.36.3"
  # Two system extensions, generated via factory.talos.dev's schematic API:
  # siderolabs/iscsi-tools and siderolabs/util-linux-tools. Talos ships
  # neither by default.
  #
  # Both were added as Longhorn's documented prerequisites, and Longhorn is
  # gone - OpenEBS Local PV Hostpath hands out directories on a mounted
  # filesystem and needs no iSCSI at all. iscsi-tools is therefore now
  # vestigial and could be dropped; util-linux-tools provides fstrim, which
  # stays useful regardless.
  #
  # Deliberately not dropped in the same change that removed Longhorn.
  # Editing this list means minting a new schematic ID through the Factory
  # API, which changes the image URL, forces a re-download and rebuilds every
  # node - a second, independent way for a run to fail, folded into a change
  # that already replaces the storage layer. Worth doing on its own, when the
  # only thing being tested is the image.
  # Minted 2026-08-31 for tailscale + util-linux-tools. iscsi-tools was dropped
  # in the same mint: it arrived as a Longhorn prerequisite, Longhorn is gone,
  # and OpenEBS Local PV Hostpath needs no iSCSI. The record above said that was
  # worth doing on its own "when the only thing being tested is the image" -
  # this is that change, and every node is rebuilt by it either way.
  schematic_id = "6e810eb45767cfabcdb7a45e389eee803045af7a9467faebde5c91164861883a"

  gitops_target_path = "clusters/management"

  # --- state database ------------------------------------------------------
  state_db_namespace = "database"
  state_db_cluster   = "tofu-state"
  state_db_name      = "tofu_state"
  state_db_owner     = "tofu"
  state_db_nodeport  = 30432
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
    hypervisors  = [for h in local.hypervisors : h.hostname]
    vm_placement = local.vm_placement
  }
}
