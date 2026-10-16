package gitops_test

import data.gitops
import rego.v1

sync(spec) := {"path": "clusters/core/flux-system/gotk-sync.yaml", "contents": {"kind": "GitRepository", "spec": spec}}

config(source_control) := {"path": "config/management.tpl.json", "contents": {"source_control": source_control}}

good := [sync({"url": "https://github.com/o/r"}), config({"repo_url": "https://github.com/o/r"})]

denies(i, words) if {
	some msg in gitops.deny with input as i
	contains(msg, words)
}

test_an_anonymous_sync_passes if {
	count(gitops.deny) == 0 with input as good
}

test_a_secret_ref_is_refused if {
	denies([sync({"url": "https://github.com/o/r", "secretRef": {"name": "flux-system"}}), good[1]], "gives the GitRepository a secretRef")
}

test_an_ssh_remote_is_refused if {
	denies([sync({"url": "ssh://git@github.com/o/r"}), good[1]], "which is not an anonymous https:// URL")
}

test_no_source_is_refused if {
	denies([good[1]], "no GitRepository is declared")
}

test_a_token_in_the_template_is_refused if {
	denies([good[0], config({"repo_url": "x", "deploy_key": "{{ op://v/i/f }}"})], "declares source_control.deploy_key")
}

test_no_source_control_in_the_template_is_refused if {
	denies([good[0]], "no longer declares source_control")
}
