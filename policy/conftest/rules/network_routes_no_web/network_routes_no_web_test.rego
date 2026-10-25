package rules.network_routes_no_web_test

import data.rules.network_routes_no_web
import rego.v1

test_a_network_that_does_not_route_the_web_is_not_read_as_doing_so if {
	count(network_routes_no_web.routes_the_web({"enable-l7-proxy": "true", "enable-gateway-api": "false"})) == 0
}

test_each_switch_is_caught_as_a_word_or_as_a_truth if {
	count(network_routes_no_web.routes_the_web({"enable-gateway-api": "true", "enable-ingress-controller": true})) == 2
}

test_the_network_routing_the_web_is_refused if {
	some msg in network_routes_no_web.deny with input as network({"enable-gateway-api": "true"})
	contains(msg, "sets enable-gateway-api")
}

network(data_) := [{"path": "clusters/n.yaml", "contents": {"kind": "ConfigMap", "metadata": {"name": "cilium-config"}, "data": data_}}]

test_a_network_as_it_should_be_passes if {
	count(network_routes_no_web.deny) == 0 with input as network({"enable-l7-proxy": "true"})
}

test_a_network_with_nothing_to_read_is_refused if {
	some msg in network_routes_no_web.deny with input as network({})
	contains(msg, "none could be read")
}
