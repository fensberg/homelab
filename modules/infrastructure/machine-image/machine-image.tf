# What the site's machines run: the Talos release, the Kubernetes release,
# and the Image Factory schematics that say which extensions each image
# carries.
#
# Its own module so that two readers take one fact. The cluster module builds
# the machines' images and templates from it; the platform module puts it in
# the cluster, where the play that rolls the machines one at a time
# (management/roll) reads what each machine should be running and compares it
# with what it is. A version is bumped here and nowhere else.

locals {
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
  # A bump reaches running machines by the roll (management/roll), one machine
  # at a time in the night's window, and new machines by their template. Until
  # 2026-10-15 a rebuild was the only way a version reached a running estate
  # (#97).
  talos_version = "v1.13.9"

  # The minor version machines may be rolled up from. Talos moves between
  # patch versions of one minor in either direction - which is what lets a
  # reverted patch bump roll machines back - and up one minor at a time,
  # forward only. A bump across a minor sets this to the minor it comes from
  # (v1.13 for v1.14.0), so that crossing is declared in the same change;
  # otherwise it is talos_version's own minor, and the roll refuses any
  # machine on another (management/roll, which refuses a value that is
  # neither).
  talos_upgrades_from = "v1.13"

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
  # branch is merged between a demolish and a build-site, which is the only
  # window in which it costs nothing. Ignite on 1.31 and the estate carries an
  # out-of-support control plane until epoch 05 exists to move it.
  #
  # tests/go/repo/versions_test.go asserts kubectl stays within one minor.
  kubernetes_version = "1.36.3"
  # Two system extensions, generated via factory.talos.dev's schematic API:
  # siderolabs/tailscale and siderolabs/util-linux-tools. Talos ships neither
  # by default. util-linux-tools provides fstrim, which stays useful whatever
  # the storage layer is.
  #
  # iscsi-tools used to be the other one. It arrived as a Longhorn
  # prerequisite, and it was deliberately NOT dropped in the change that
  # removed Longhorn: editing this list mints a new schematic, which changes
  # the image URL and rebuilds every node - a second way for a run to fail,
  # folded into a change that already replaced the storage layer. It went in
  # its own mint, on 2026-08-31, once the image was the only thing being
  # tested. OpenEBS Local PV Hostpath hands out directories on a mounted
  # filesystem and needs no iSCSI at all.
  #
  # This paragraph used to say both things at once - that the schematic held
  # iscsi-tools and that iscsi-tools had been dropped - because the second
  # half was appended when the mint happened and the first half was never
  # updated. The clerk caught it.
  #
  # HOW TO MINT ONE, because a 64-character literal with no instructions is a
  # value a fork cannot change and cannot even verify (#360). POST the
  # customization to the Factory and it answers with the id, which is a content
  # address of exactly that customization:
  #
  #     curl -X POST --data-binary @- https://factory.talos.dev/schematics <<'YAML'
  #     customization:
  #       systemExtensions:
  #         officialExtensions:
  #           - siderolabs/tailscale
  #           - siderolabs/util-linux-tools
  #     YAML
  #
  # And to read back what an id contains, which is the half that makes the
  # literal below reviewable rather than merely present:
  #
  #     curl -s https://factory.talos.dev/schematics/<id>
  #
  # Changing this forces a re-download and rebuilds every node, so it is a
  # change to make on its own - see the paragraph above.
  schematic_id = "6e810eb45767cfabcdb7a45e389eee803045af7a9467faebde5c91164861883a"

  # The same image without the overlay extension, for the untrusted zone.
  #
  # This is the layer that answers "what does the machine itself reach", and it
  # cannot be a configuration setting: every node carrying the tailscale
  # extension from one shared schematic is how a compromised pod on any of them
  # reaches the hypervisor, the workstation and every other site, because the
  # tailnet policy is still the default allow-all. Leaving the extension out of
  # the image is the only form of that answer which a machine cannot talk its
  # way back into.
  #
  # Minted from the schematic above with siderolabs/tailscale removed and
  # siderolabs/util-linux-tools kept; the Factory ids are content-addressed, so
  # this one is exactly that customization and nothing else. Verified against
  # factory.talos.dev/schematics/<id>, which returns the customization it was
  # minted from.
  #
  # Both ids resolve their extensions from talos_version above, so the two
  # images move together rather than drifting apart on a version bump - and #97
  # applies to both: an image change reaches a running estate only through a
  # rebuild.
  dmz_schematic_id = "70d243b7e2cbe699e4db5e73356a2add6b4bb8e34eadba9db22c823110e79099"
}

output "talos_version" {
  value = local.talos_version
}

output "talos_upgrades_from" {
  value = local.talos_upgrades_from
}

output "kubernetes_version" {
  value = local.kubernetes_version
}

output "schematic_id" {
  value = local.schematic_id
}

output "dmz_schematic_id" {
  value = local.dmz_schematic_id
}

# The image a running trusted machine is upgraded to: Talos's installer for
# the schematic its disk image was built from, at this release.
output "installer_image" {
  value = "factory.talos.dev/installer/${local.schematic_id}:${local.talos_version}"
}
