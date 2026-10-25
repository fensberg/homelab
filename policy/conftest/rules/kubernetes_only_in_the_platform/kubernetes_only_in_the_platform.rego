# Nothing that builds the machines talks to Kubernetes.
#
# WHAT THIS GUARDS, and it is not style. A kubernetes_* resource planned
# alongside the machines starts in the first wave of the apply, while the API
# server is still pulling its static pods; the provider gets `connection
# refused` and an ignition tears the estate back down. That happened: an
# application's namespace and `kubernetes_secret.runner_vars` went that way
# in one run, thirty seconds after bootstrap. And on the way down, deleting
# the flux-system namespace hung on Flux's finalizers until the destroy gave
# up having destroyed nothing, the machines included.
#
# Now it is the shape of the site (docs/epochs/02-abstraction.md, "Inside a
# site, the split is two roots sharing one config"): everything that talks to
# the cluster's API is in the platform root, applied only after the cluster
# root's last step has returned and never destroyed. What can still go wrong
# is a resource in the wrong root.
#
# ON WHAT TOFU WROTE. A root or module that means to talk to Kubernetes is
# initialised with its provider, and `tofu init` locks every provider it was
# initialised with. So a lock that locks the hypervisor's provider - the root
# or module that builds the machines - must lock no Kubernetes provider. One
# written without updating its lock does not plan at all, and the rules over
# the plan refuse that too.
package rules.kubernetes_only_in_the_platform

import data.lib
import rego.v1

hypervisor := "registry.opentofu.org/bpg/proxmox"

kubernetes := "registry.opentofu.org/hashicorp/kubernetes"

builds_machines contains path if {
	some [path, source] in lib.provider_locks
	source == hypervisor
}

talks_to_kubernetes contains path if {
	some [path, source] in lib.provider_locks
	source == kubernetes
}

deny contains msg if {
	count(builds_machines) == 0
	msg := sprintf("no provider lock locks %s, so nothing here knows what builds the machines and this checked nothing", [hypervisor])
}

deny contains msg if {
	count(talks_to_kubernetes) == 0
	msg := sprintf("no provider lock locks %s, so nothing talks to the cluster's API and this checked nothing", [kubernetes])
}

deny contains msg if {
	some path in builds_machines & talks_to_kubernetes
	msg := sprintf("%s locks both the hypervisor's provider and Kubernetes's. Everything that talks to the cluster's API belongs to the platform: it is applied only after every node is Ready, and it is never destroyed. Beside the machines it would be planned before the cluster exists, and a teardown would wait on the cluster to delete it.", [path])
}
