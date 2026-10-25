package rules.front_door_gets_no_certificates_test

import data.rules.front_door_gets_no_certificates
import rego.v1

test_a_proxy_told_no_way_of_getting_a_certificate_has_none if {
	quiet := {
		"certificatesResolvers": {},
		"persistence": {"enabled": false},
		"ports": {"websecure": {"expose": {"default": false}}},
		"additionalArguments": ["--log.level=INFO"],
	}
	count(front_door_gets_no_certificates.gets_certificates(quiet)) == 0
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
		count(front_door_gets_no_certificates.gets_certificates(values)) > 0
	}
}

test_the_front_door_getting_a_certificate_is_refused if {
	some msg in front_door_gets_no_certificates.deny with input as front_door({"certificatesResolvers": {"mine": {}}})
	contains(msg, "certificatesResolvers names one")
}

front_door(values) := [{"path": "clusters/t.yaml", "contents": {"kind": "HelmRelease", "metadata": {"name": "traefik"}, "spec": {"values": values}}}]

test_a_front_door_as_it_should_be_passes if {
	count(front_door_gets_no_certificates.deny) == 0 with input as front_door({"ports": {}})
}

test_a_front_door_with_nothing_to_read_is_refused if {
	some msg in front_door_gets_no_certificates.deny with input as front_door({})
	contains(msg, "its settings could not be read")
}

test_no_front_door_is_refused if {
	some msg in front_door_gets_no_certificates.deny with input as []
	contains(msg, "0 HelmRelease named traefik were found")
}
