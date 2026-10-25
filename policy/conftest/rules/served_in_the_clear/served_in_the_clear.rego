# Nothing a device reaches is served in the clear.
#
# What a device reaches is a Service that sets its own address: the estate
# routes an enrolled device to that address and to no other, so those
# Services are the whole of what is served to anything outside the cluster.
# Each port of one is held to one of two things. Over TCP it is HTTPS, and
# says so in its name, which is the one place a Service can say it. Over
# UDP it speaks a protocol of its own that no certificate is served on, and
# it is named here as outside this, by the rule's own warning, and not passed
# in silence.
#
# What this cannot see is whether the pods behind a port named for HTTPS
# serve it. That is a fact about a running site and is asked of one nightly.
# What the cluster's own parts say to each other is not a device reaching
# anything, and is the network's to encrypt
# (docs/epochs/11-black-box-white-glove.md).
package rules.served_in_the_clear

import data.lib
import rego.v1

# What a device reaches: a Service that sets its own address.
served contains [f.path, f.contents.metadata.name, object.get(f.contents.spec, "ports", [])] if {
	some f in lib.objects
	f.contents.kind == "Service"
	address := object.get(f.contents.spec, "clusterIP", "")
	not address in {"", "None"}
}

deny contains msg if {
	some [path, service, ports] in served
	some p in ports
	object.get(p, "protocol", "") != "UDP"
	object.get(p, "name", "") != "https"
	msg := sprintf("%s: the Service %s is reached by a device and its port %q is not served over HTTPS. Nothing a device reaches is served in the clear. Serve the port with the certificate the site is handed and name it https, which is how a Service says so.", [path, service, object.get(p, "name", "")])
}

deny contains msg if {
	some [path, service, ports] in served
	count(ports) == 0
	msg := sprintf("%s: the Service %s is reached by a device and its port \"\" is not served over HTTPS. Nothing a device reaches is served in the clear. Name its ports, and serve each with the certificate the site is handed.", [path, service])
}

warn contains msg if {
	some [path, service, ports] in served
	some p in ports
	p.protocol == "UDP"
	msg := sprintf("outside this rule - %s: the Service %s serves %q over UDP, a protocol of its own that no certificate is served on.", [path, service, object.get(p, "name", "")])
}
