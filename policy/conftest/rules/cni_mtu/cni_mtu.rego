# The pod MTU is pinned, and the rendered manifest carries the number.
#
# Since v1.17 Cilium hands every pod the LOWEST link MTU it finds on the node.
# These nodes carry `tailscale0` at 1280, which is not how a pod reaches
# anything: pod traffic leaves over eth0 at 1450. Detection therefore sizes
# every pod for an interface it does not use.
#
# The damage is not only lost throughput. 1280 minus Cilium's tunnel overhead
# is 1230, below the 1252-byte initial packet quic-go insists on, so every
# QUIC handshake from the tunnel connector timed out and it fell back to
# HTTP/2 - which carries no UDP, and so cannot serve the game server (#455).
# Cilium tracks the regression as cilium/cilium#37529, open and unfixed on
# 1.18, and `MTU` is the escape hatch its maintainers point at.
#
# So the value is declared, and the committed render has to agree with it: a
# value edited without `task render-cni` is a pin that changes nothing, and
# the cluster keeps whatever the last render said.
package rules.cni_mtu

import data.lib
import rego.v1

agents := [f | some f in lib.objects; f.contents.kind == "DaemonSet"; f.contents.metadata.name == "cilium"]

dir(path) := concat("/", array.slice(parts, 0, count(parts) - 1)) if parts := split(path, "/")

config := [f | some f in lib.objects; f.contents.kind == "ConfigMap"; f.contents.metadata.name == "cilium-config"]

values := [f |
	some agent in agents
	some f in lib.named("cilium-values.yaml")
	f.path == $"{dir(agent.path)}/cilium-values.yaml"
]

deny contains msg if {
	count(values) == 0
	msg := "no cilium-values.yaml is beside the rendered CNI, so the values it was rendered from cannot be read"
}

deny contains msg if {
	some f in values
	not is_number(object.get(f.contents, "MTU", null))
	msg := $"{f.path} pins no MTU. Cilium then detects one, and on these nodes it detects tailscale0's 1280 and gives it to every pod. That is below what quic-go will start a connection with once Cilium's tunnel overhead comes off it, so the tunnel connector cannot use QUIC, and without QUIC it carries no UDP at all (#455, cilium/cilium#37529). Pin MTU to the MTU of the interface pods actually leave by, and re-render."
}

deny contains msg if {
	some f in values
	f.contents.MTU <= 1280
	msg := $"the pinned MTU is {f.contents.MTU}, which leaves less than quic-go's 1252-byte initial packet once Cilium's tunnel overhead comes off it. The tunnel connector would fall back to HTTP/2 and carry no UDP (#455)."
}

deny contains msg if {
	count(config) != 1
	msg := $"{count(config)} ConfigMap named cilium-config were found, so what the committed render runs cannot be read"
}

deny contains msg if {
	some f in config
	not f.contents.data.mtu
	msg := $"{f.path} carries no mtu in its config, so the pinned value never reached the manifest. Re-render with `task render-cni`."
}

deny contains msg if {
	some v in values
	some f in config
	rendered := f.contents.data.mtu
	rendered != $"{v.contents.MTU}"
	msg := $"cilium-values.yaml pins MTU {v.contents.MTU} and the committed manifest says {rendered}. The cluster runs the manifest, so the pin is not in effect. Re-render with `task render-cni`."
}
