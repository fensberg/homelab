package rules.gitops_test

import data.rules.gitops
import rego.v1

sync(spec) := {"path": "clusters/core/flux-system/gotk-sync.yaml", "contents": {"kind": "GitRepository", "spec": spec}}

config(source_control) := {"path": "config/management.tpl.json", "contents": {"source_control": source_control, "state_backup": {"recipient": "{{ op://estate/state_backup/recipient }}"}}}

good := [sync({"url": "https://github.com/o/r"}), config({"repo_url": "https://github.com/o/r"})]

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_an_anonymous_sync_passes if {
	count(gitops.deny) == 0 with input as good
}

test_a_secret_ref_is_refused if {
	denies(gitops.deny, "gives the GitRepository a secretRef") with input as [sync({"url": "https://github.com/o/r", "secretRef": {"name": "flux-system"}}), good[1]]
}

test_an_ssh_remote_is_refused if {
	denies(gitops.deny, "which is not an anonymous https:// URL") with input as [sync({"url": "ssh://git@github.com/o/r"}), good[1]]
}

test_no_source_is_refused if {
	denies(gitops.deny, "no GitRepository is declared") with input as [good[1]]
}

test_a_token_in_the_template_is_refused if {
	denies(gitops.deny, "declares source_control.deploy_key") with input as [good[0], config({"repo_url": "x", "deploy_key": "{{ op://v/i/f }}"})]
}

test_no_source_control_in_the_template_is_refused if {
	denies(gitops.deny, "no longer declares source_control") with input as [good[0]]
}

test_the_break_glass_identity_in_the_template_is_refused if {
	i := [good[0], {"path": "config/management.tpl.json", "contents": {"source_control": {"repo_url": "x"}, "state_backup": {"recipient": "{{ op://estate/state_backup/recipient }}", "identity": "{{ op://estate/state_backup/identity }}"}}}]
	denies(gitops.deny, `references "state_backup/identity"`) with input as i
}

test_a_template_without_the_recipient_is_refused if {
	denies(gitops.deny, "no longer references state_backup/recipient") with input as [good[0], {"path": "config/management.tpl.json", "contents": {"source_control": {"repo_url": "x"}}}]
}
