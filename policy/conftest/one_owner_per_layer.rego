# Each layer a request passes has one owner, and a tool that could do a
# neighbour's job is held from doing it.
#
# Three of the tools between a device and an application can do more than
# the one thing each is here for: the network can route the web, the front
# door can get certificates, and the vendor's edge can hold one. Each of
# those is somebody else's job (docs/epochs/04-observability.md), and a
# second owner of a job is how a site comes to have two certificates for a
# name and nobody sure which is served. The first two are held here, by
# reading what each tool is told. The third is the estate's, and is held
# where the estate is tested: a tunnel that serves a public name is refused
# by management/estate's own suite.
package rules.one_owner_per_layer

import rego.v1

object_named(kind, name) := [f | some f in input; f.contents.kind == kind; f.contents.metadata.name == name]

# The network does not route the web.

routes_the_web(settings) := {key |
	some key in ["enable-gateway-api", "enable-ingress-controller"]
	as_text(object.get(settings, key, "")) == "true"
}

as_text(v) := v if is_string(v)

as_text(v) := "true" if v == true

as_text(v) := "" if {
	not is_string(v)
	v != true
}

network := object_named("ConfigMap", "cilium-config")

deny contains msg if {
	count(network) != 1
	msg := sprintf("%d ConfigMap named cilium-config were found, so the network's settings could not be read", [count(network)])
}

deny contains msg if {
	some f in network
	count(object.get(f.contents, "data", {})) == 0
	msg := sprintf("%s declares the network's settings and none could be read, so this checked nothing", [f.path])
}

deny contains msg if {
	some f in network
	some key in routes_the_web(object.get(f.contents, "data", {}))
	msg := sprintf("%s sets %s. The network connects pods and says which may speak to which; taking a hostname to an application is the front door's, and two things routing the web is two places a name can be answered from. If the network is to take the job over, that is a decision for the epoch record and the front door goes.", [f.path, key])
}

# The front door gets no certificates.

gets_certificates(values) := ways if {
	ways := (({"certificatesResolvers names one" | count(object.get(values, "certificatesResolvers", {})) > 0} |
		{"persistence gives it a disk, which it has only certificates of its own to keep on" | object.get(values, ["persistence", "enabled"], false) == true}) |
		{sprintf("%s speaks of %s", [where, word]) |
			some where in ["additionalArguments", "env", "envFrom", "ports", "volumes", "additionalVolumeMounts"]
			v := values[where]
			some word in ["certificatesresolvers", "certresolver", "acme", "dns_api_token", "dns-token"]
			speaks_of(v, word)
		})
}

# speaks_of is whether a word is anywhere in a setting, as a key or a value.
speaks_of(v, word) if {
	walk(v, [path, leaf])
	some s in array.concat([leaf | is_string(leaf)], [k | some k in path; is_string(k)])
	contains(lower(s), word)
}

front_door := object_named("HelmRelease", "traefik")

deny contains msg if {
	count(front_door) != 1
	msg := sprintf("%d HelmRelease named traefik were found, so the front door's settings could not be read", [count(front_door)])
}

deny contains msg if {
	some f in front_door
	count(object.get(f.contents.spec, "values", {})) == 0
	msg := sprintf("%s declares the front door and its settings could not be read, so this checked nothing", [f.path])
}

deny contains msg if {
	some f in front_door
	some way in gets_certificates(object.get(f.contents.spec, "values", {}))
	msg := sprintf("%s: %s. The front door serves certificates and gets none: getting them is cert-manager's, which holds the credential for the domain and is the one place a certificate is renewed. A proxy that gets its own keeps them on one disk in one pod, and is a second owner of every name it serves.", [f.path, way])
}
