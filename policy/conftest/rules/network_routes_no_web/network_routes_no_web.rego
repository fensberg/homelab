# The network does not route the web.
#
# Each layer a request passes has one owner, and a tool that could do a
# neighbour's job is held from doing it. Three of the tools between a device
# and an application can do more than the one thing each is here for: the
# network can route the web, the front door can get certificates, and the
# vendor's edge can hold one. Each of those is somebody else's job
# (docs/epochs/04-observability.md), and a second owner of a job is how a site
# comes to have two certificates for a name and nobody sure which is served.
# The first two are held by reading what each tool is told (rules.front_door_gets_no_certificates). The
# third is the estate's, and is held where the estate is tested: a tunnel that
# serves a public name is refused by management/estate's own suite.
package rules.network_routes_no_web

import data.lib
import rego.v1

object_named(kind, name) := [f | some f in lib.objects; f.contents.kind == kind; f.contents.metadata.name == name]

routes_the_web(settings) := {key |
	some key in ["enable-gateway-api", "enable-ingress-controller"]
	as_text(object.get(settings, key, "")) == "true"
}

as_text(v) := v if is_string(v)

as_text(true) := "true"

as_text(v) := "" if {
	not is_string(v)
	v != true
}

network := object_named("ConfigMap", "cilium-config")

deny contains msg if {
	count(network) != 1
	msg := $"{count(network)} ConfigMap named cilium-config were found, so the network's settings could not be read"
}

deny contains msg if {
	some f in network
	count(object.get(f.contents, "data", {})) == 0
	msg := $"{f.path} declares the network's settings and none could be read, so this checked nothing"
}

deny contains msg if {
	some f in network
	some key in routes_the_web(object.get(f.contents, "data", {}))
	msg := $"{f.path} sets {key}. The network connects pods and says which may speak to which; taking a hostname to an application is the front door's, and two things routing the web is two places a name can be answered from. If the network is to take the job over, that is a decision for the epoch record and the front door goes."
}
