package releases_test

import data.releases
import rego.v1

digest := concat("", ["sha256:", concat("", ["a" | some _ in numbers.range(1, 64)])])

source(r) := [
	{"path": "clusters/x.yaml", "contents": {"kind": "ConfigMap", "metadata": {"name": "other"}}},
	{"path": "clusters/x.yaml", "contents": {"kind": "OCIRepository", "metadata": {"name": "thing"}, "spec": {"ref": r}}},
]

problems(r) := found if {
	found := releases.deny with input as source(r)
}

says(r, words) if {
	some msg in problems(r)
	contains(msg, words)
}

test_a_tag_and_a_digest_pass if {
	count(problems({"tag": "1.0.0-1", "digest": digest})) == 0
}

test_a_tag_alone_is_not_pinned if {
	count(problems({"tag": "1.0.0-1"})) == 1
	says({"tag": "1.0.0-1"}, "is not pinned by digest")
}

test_a_digest_alone_has_no_tag if {
	count(problems({"digest": digest})) == 1
	says({"digest": digest}, "has no tag")
}

test_a_digest_cut_short_is_not_pinned if {
	says({"tag": "1.0.0-1", "digest": "sha256:abc"}, "is not pinned by digest")
}

test_nothing_pinned_is_both if {
	count(problems({"semver": "*"})) == 2
}

test_flux_own_definitions_are_not_a_release if {
	count(releases.deny) == 0 with input as [{"path": "clusters/core/flux-system/gotk-components.yaml", "contents": {"kind": "OCIRepository", "metadata": {"name": "x"}}}]
}
