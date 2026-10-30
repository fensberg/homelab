# The front door gets no certificates.
#
# Each layer a request passes has one owner, and a tool that could do a
# neighbour's job is held from doing it. Three of the tools between a device
# and an application can do more than the one thing each is here for: the
# network can route the web, the front door can get certificates, and the
# vendor's edge can hold one. Each of those is somebody else's job
# (docs/epochs/04-observability.md), and a second owner of a job is how a site
# comes to have two certificates for a name and nobody sure which is served.
# The first two are held by reading what each tool is told (rules.network_routes_no_web). The
# third is the estate's, and is held where the estate is tested: a tunnel that
# serves a public name is refused by management/estate's own suite.
package rules.front_door_gets_no_certificates

import data.lib
import rego.v1

object_named(kind, name) := [f | some f in lib.objects; f.contents.kind == kind; f.contents.metadata.name == name]

gets_certificates(values) := ({"certificatesResolvers names one" | count(object.get(values, "certificatesResolvers", {})) > 0} | {"persistence gives it a disk, which it has only certificates of its own to keep on" | values.persistence.enabled == true}) | {$"{where} speaks of {word}" |
	some where in ["additionalArguments", "env", "envFrom", "ports", "volumes", "additionalVolumeMounts"]
	v := values[where]
	some word in ["certificatesresolvers", "certresolver", "acme", "dns_api_token", "dns-token"]
	speaks_of(v, word)
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
	msg := $"{count(front_door)} HelmRelease named traefik were found, so the front door's settings could not be read"
}

deny contains msg if {
	some f in front_door
	count(object.get(f.contents.spec, "values", {})) == 0
	msg := $"{f.path} declares the front door and its settings could not be read, so this checked nothing"
}

deny contains msg if {
	some f in front_door
	some way in gets_certificates(object.get(f.contents.spec, "values", {}))
	msg := $"{f.path}: {way}. The front door serves certificates and gets none: getting them is cert-manager's, which holds the credential for the domain and is the one place a certificate is renewed. A proxy that gets its own keeps them on one disk in one pod, and is a second owner of every name it serves."
}
