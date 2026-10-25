package rules.kubernetes_only_in_the_platform_test

import data.rules.kubernetes_only_in_the_platform
import rego.v1

lock(path, sources) := {"path": path, "contents": {"provider": {s: [{"version": "1.0.0", "hashes": ["h1:x"]}] | some s in sources}}}

proxmox := "registry.opentofu.org/bpg/proxmox"

k8s := "registry.opentofu.org/hashicorp/kubernetes"

denies(msgs, words) if {
	some msg in msgs
	contains(msg, words)
}

test_each_on_its_own_side_passes if {
	count(kubernetes_only_in_the_platform.deny) == 0 with input as [lock("roots/cluster/.terraform.lock.hcl", [proxmox, "registry.opentofu.org/siderolabs/talos"]), lock("roots/platform/.terraform.lock.hcl", [k8s])]
}

test_kubernetes_beside_the_machines_is_refused if {
	denies(kubernetes_only_in_the_platform.deny, "roots/cluster/.terraform.lock.hcl locks both the hypervisor's provider and Kubernetes's") with input as [lock("roots/cluster/.terraform.lock.hcl", [proxmox, k8s]), lock("roots/platform/.terraform.lock.hcl", [k8s])]
}

test_a_file_without_hashes_is_not_a_lock if {
	denies(kubernetes_only_in_the_platform.deny, "so nothing here knows what builds the machines") with input as [{"path": "x.hcl", "contents": {"provider": {proxmox: [{"version": "1"}]}}}, lock("p.hcl", [k8s])]
}

test_nothing_talking_to_kubernetes_is_refused if {
	denies(kubernetes_only_in_the_platform.deny, "nothing talks to the cluster's API") with input as [lock("c.hcl", [proxmox])]
}
