package one_owner_per_layer_test

import data.one_owner_per_layer
import rego.v1

test_a_network_that_does_not_route_the_web_is_not_read_as_doing_so if {
	count(one_owner_per_layer.routes_the_web({"enable-l7-proxy": "true", "enable-gateway-api": "false"})) == 0
}

test_each_switch_is_caught_as_a_word_or_as_a_truth if {
	count(one_owner_per_layer.routes_the_web({"enable-gateway-api": "true", "enable-ingress-controller": true})) == 2
}

test_a_proxy_told_no_way_of_getting_a_certificate_has_none if {
	quiet := {
		"certificatesResolvers": {},
		"persistence": {"enabled": false},
		"ports": {"websecure": {"expose": {"default": false}}},
		"additionalArguments": ["--log.level=INFO"],
	}
	count(one_owner_per_layer.gets_certificates(quiet)) == 0
}

test_each_way_of_getting_a_certificate_is_caught if {
	every values in [
		{"certificatesResolvers": {"mine": {}}},
		{"persistence": {"enabled": true}},
		{"additionalArguments": ["--certificatesresolvers.mine.acme.dnschallenge=true"]},
		{"env": [{"name": "CF_DNS_API_TOKEN"}]},
		{"envFrom": [{"secretRef": {"name": "dns-token"}}]},
		{"ports": {"websecure": {"tls": {"certResolver": "mine"}}}},
	] {
		count(one_owner_per_layer.gets_certificates(values)) > 0
	}
}

layers(data_, values) := [
	{"path": "clusters/n.yaml", "contents": {"kind": "ConfigMap", "metadata": {"name": "cilium-config"}, "data": data_}},
	{"path": "clusters/t.yaml", "contents": {"kind": "HelmRelease", "metadata": {"name": "traefik"}, "spec": {"values": values}}},
]

test_the_layers_as_they_should_be_pass if {
	count(one_owner_per_layer.deny) == 0 with input as layers({"enable-l7-proxy": "true"}, {"ports": {}})
}

test_the_network_routing_the_web_is_refused if {
	some msg in one_owner_per_layer.deny with input as layers({"enable-gateway-api": "true"}, {"ports": {}})
	contains(msg, "sets enable-gateway-api")
}

test_the_front_door_getting_a_certificate_is_refused if {
	some msg in one_owner_per_layer.deny with input as layers({"enable-l7-proxy": "true"}, {"certificatesResolvers": {"mine": {}}})
	contains(msg, "certificatesResolvers names one")
}

test_nothing_to_read_is_refused if {
	some msg in one_owner_per_layer.deny with input as layers({}, {})
	contains(msg, "none could be read")
	some other in one_owner_per_layer.deny with input as layers({}, {})
	contains(other, "its settings could not be read")
}
