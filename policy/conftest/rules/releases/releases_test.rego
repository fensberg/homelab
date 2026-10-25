package rules.releases_test

import data.rules.releases
import rego.v1

digest := concat("", ["sha256:", concat("", ["a" | some _ in numbers.range(1, 64)])])

source(r) := [
	{"path": "clusters/x.yaml", "contents": {"kind": "ConfigMap", "metadata": {"name": "other"}}},
	{"path": "clusters/x.yaml", "contents": {"kind": "OCIRepository", "metadata": {"name": "thing"}, "spec": {"ref": r}}},
]

says(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_a_tag_and_a_digest_pass if {
	count(releases.deny) == 0 with input as source({"tag": "1.0.0-1", "digest": digest})
}

test_a_tag_alone_is_not_pinned if {
	count(releases.deny) == 1 with input as source({"tag": "1.0.0-1"})
	says(releases.deny, "is not pinned by digest") with input as source({"tag": "1.0.0-1"})
}

test_a_digest_alone_has_no_tag if {
	count(releases.deny) == 1 with input as source({"digest": digest})
	says(releases.deny, "has no tag") with input as source({"digest": digest})
}

test_a_digest_cut_short_is_not_pinned if {
	says(releases.deny, "is not pinned by digest") with input as source({"tag": "1.0.0-1", "digest": "sha256:abc"})
}

test_nothing_pinned_is_both if {
	count(releases.deny) == 2 with input as source({"semver": "*"})
}

test_flux_own_definitions_are_not_a_release if {
	count(releases.deny) == 0 with input as [{"path": "clusters/core/flux-system/gotk-components.yaml", "contents": {"kind": "OCIRepository", "metadata": {"name": "x"}}}]
}
