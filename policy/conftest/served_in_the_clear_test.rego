package served_in_the_clear_test

import data.served_in_the_clear
import rego.v1

service(name, spec) := {"path": "f.yaml", "contents": {"kind": "Service", "metadata": {"name": name}, "spec": spec}}

services := [
	service("allocated", {"ports": [{"name": "http"}]}),
	service("headless", {"clusterIP": "None", "ports": [{"name": "http"}]}),
	service("front-door", {"clusterIP": "${ADDRESS_FRONT_DOOR}", "ports": [{"name": "https", "protocol": "TCP"}, {"name": "plain"}, {"name": "own", "protocol": "UDP"}]}),
	service("side-door", {"clusterIP": "${ADDRESS_SIDE_DOOR}"}),
	{"path": "f.yaml", "contents": {"kind": "ConfigMap", "metadata": {"name": "other"}, "spec": {"clusterIP": "1.2.3.4", "ports": [{"name": "http"}]}}},
]

test_a_port_not_named_for_https_and_a_service_naming_none_are_each_refused if {
	refused := served_in_the_clear.deny with input as services
	count(refused) == 2
	some a in refused
	contains(a, `front-door is reached by a device and its port "plain"`)
	some b in refused
	contains(b, `side-door is reached by a device and its port ""`)
}

test_a_udp_port_is_named_as_outside_the_rule_and_not_passed_in_silence if {
	said := served_in_the_clear.warn with input as services
	count(said) == 1
	some w in said
	contains(w, `front-door serves "own" over UDP`)
}

test_only_a_port_named_https_is_taken_as_served_over_it if {
	refused := served_in_the_clear.deny with input as [service("x", {"clusterIP": "${A}", "ports": [{"name": "HTTPS", "protocol": "TCP"}, {"name": "http", "protocol": "SCTP"}]})]
	count(refused) == 2
}
