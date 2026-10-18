# Every release a site runs is pinned by digest.
#
# A release is published under a tag, and a tag in a registry is a pointer
# anyone who can push may move. Pinned by tag alone, the bytes a site runs
# could change with no pull request, no review and nothing in git recording
# it - which is the property a site's block exists to give it. So every
# OCIRepository names a digest, and the tag beside it is for the people
# reading the diff.
#
# Found by what it is, so a source added anywhere Flux reads is held to it
# without this rule being told. Flux's own CRDs are vendored verbatim and
# describe the kind rather than being one.
package rules.releases

import data.lib
import rego.v1

digest := `^sha256:[0-9a-f]{64}$`

sources contains f if {
	some f in lib.objects
	f.contents.kind == "OCIRepository"
	not endswith(f.path, "gotk-components.yaml")
}

ref(f) := object.get(object.get(f.contents, "spec", {}), "ref", {})

deny contains msg if {
	some f in sources
	given := object.get(ref(f), "digest", "")
	not regex.match(digest, given)
	msg := sprintf("%s: the release %q is not pinned by digest (ref.digest is %q). A tag can be moved in the registry without a pull request, so the site would run whatever it points at today. Pin the digest the fabricator reported for this release.", [f.path, f.contents.metadata.name, given])
}

deny contains msg if {
	some f in sources
	object.get(ref(f), "tag", "") == ""
	msg := sprintf("%s: the release %q has no tag, so a diff moving its digest says nothing a person can read. Name the version beside the digest.", [f.path, f.contents.metadata.name])
}
