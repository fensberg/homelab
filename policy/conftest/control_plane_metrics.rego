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
package control_plane_metrics

import rego.v1

components := {
	"kubeScheduler": ["bind-address", "the scheduler binds to 127.0.0.1 until bind-address says otherwise"],
	"kubeControllerManager": ["bind-address", "the controller-manager binds to 127.0.0.1 until bind-address says otherwise"],
	"kubeEtcd": ["listen-metrics-urls", "etcd serves no metrics until listen-metrics-urls asks for them"],
}

stack := [f | some f in input; f.contents.kind == "HelmRelease"; f.contents.metadata.name == "kube-prometheus-stack"]

# Where OpenTofu says a setting, as the expression it writes, with the lines
# that are comments inside it left out.
settings contains [f.path, setting] if {
	some f in input
	endswith(f.path, ".tf")
	walk(f.contents, [_, v])
	is_string(v)
	some [setting, _] in components
	regex.match(sprintf(`(?m)^[^#\n]*\b%s\s*=`, [setting]), v)
}

exposed(setting) if {
	some [_, s] in settings
	s == setting
}

machine_config := concat(", ", sort({p | some [p, _] in settings})) if count(settings) > 0

machine_config := "the OpenTofu" if count(settings) == 0

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
	some component, [setting, what] in components
	enabled(component)
	not exposed(setting)
	msg := sprintf("the chart scrapes %s, and %s does not contain %q. %s, so the target would sit red from the moment this deploys - and a red target nobody can fix is how people learn to ignore red targets.", [component, machine_config, setting, what])
}

deny contains msg if {
	some component, [setting, _] in components
	stack[0].contents.spec.values[component]
	not enabled(component)
	exposed(setting)
	msg := sprintf("%s contains %q and the chart does not scrape %s. That leaves a port open on every control-plane node with nothing reading it. If the scrape is being removed deliberately, remove the machine configuration with it.", [machine_config, setting, component])
}
