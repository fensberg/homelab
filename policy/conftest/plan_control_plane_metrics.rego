# Scraping a control-plane component takes two changes, in two tiers, and
# either alone is wrong.
#
# Talos binds the scheduler and the controller-manager to 127.0.0.1 and leaves
# etcd's metrics listener off. The chart cannot change that; a machine
# configuration change can, and it arrives through a converge rather than
# through Flux. So the two halves live in different files, read by different
# tools, applied by different mechanisms.
#
# Each half alone fails quietly in its own direction. Enable the chart without
# the machine configuration and three targets sit red, which teaches everyone
# to ignore red targets. Apply the machine configuration without the chart and
# three ports are open on every control-plane node with nothing reading them -
# including etcd's unauthenticated one.
#
# The estate ran the first half of that for a while: the stack shipped with
# these switched off and a comment promising the machine configuration later.
#
# ON THE PLAN for the machine configuration's half: what every control plane
# would be sent, read as Talos reads its patches.
package rules.plan_control_plane_metrics

import data.lib
import rego.v1

components := {
	"kubeScheduler": [["cluster", "scheduler", "extraArgs", "bind-address"], "the scheduler binds to 127.0.0.1 until bind-address says otherwise"],
	"kubeControllerManager": [["cluster", "controllerManager", "extraArgs", "bind-address"], "the controller-manager binds to 127.0.0.1 until bind-address says otherwise"],
	"kubeEtcd": [["cluster", "etcd", "extraArgs", "listen-metrics-urls"], "etcd serves no metrics until listen-metrics-urls asks for them"],
}

stack := [f | some f in lib.objects; f.contents.kind == "HelmRelease"; f.contents.metadata.name == "kube-prometheus-stack"]

control_planes contains r if {
	some [_, r] in lib.plan
	r.type == "talos_machine_configuration"
	r.values.machine_type == "controlplane"
}

# told is whether a machine's patches set a setting.
told(r, path) if {
	some patch in object.get(r.values, "config_patches", [])
	is_string(patch)
	object.get(yaml.unmarshal(patch), path, null) != null
}

exposed(path) if {
	count(control_planes) > 0
	every r in control_planes {
		told(r, path)
	}
}

opened(path) if {
	some r in control_planes
	told(r, path)
}

setting(path) := concat(".", path)

deny contains msg if {
	count(control_planes) == 0
	msg := "no control plane's machine configuration is in any plan, so whether the control plane is opened to the chart cannot be read"
}

deny contains msg if {
	count(stack) != 1
	msg := sprintf("%d HelmRelease named kube-prometheus-stack were found, so whether the chart scrapes the control plane cannot be read", [count(stack)])
}

deny contains msg if {
	some component, _ in components
	some f in stack
	not f.contents.spec.values[component]
	msg := sprintf("the monitoring HelmRelease says nothing about %s at all, so this rule is reading the wrong shape and proves nothing", [component])
}

enabled(component) if stack[0].contents.spec.values[component].enabled == true

deny contains msg if {
	some component, [path, what] in components
	enabled(component)
	not exposed(path)
	msg := sprintf("the chart scrapes %s, and not every control plane's configuration sets %s. %s, so the target would sit red from the moment this deploys - and a red target nobody can fix is how people learn to ignore red targets.", [component, setting(path), what])
}

deny contains msg if {
	some component, [path, _] in components
	stack[0].contents.spec.values[component]
	not enabled(component)
	opened(path)
	msg := sprintf("a control plane's configuration sets %s and the chart does not scrape %s. That leaves a port open on every control-plane node with nothing reading it. If the scrape is being removed deliberately, remove the machine configuration with it.", [setting(path), component])
}
