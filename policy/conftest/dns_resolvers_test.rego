package rules.dns_resolvers_test

import data.rules.dns_resolvers
import rego.v1

filler := [{"path": sprintf("modules/x/f%d.tf", [i]), "contents": {}} | some i in numbers.range(1, 10)]

declared := {"path": "modules/c/variables.tf", "contents": {"locals": [{"dns_resolvers": ["1.1.1.1", "1.0.0.1"]}]}}

with_file(contents) := array.concat(filler, [declared, {"path": "modules/c/compute.tf", "contents": contents}])

denies(i, words) if {
	some msg in dns_resolvers.deny with input as i
	contains(msg, words)
}

test_resolvers_read_from_the_declaration_pass if {
	count(dns_resolvers.deny) == 0 with input as with_file({"resource": {"vm": {"a": [{"initialization": [{"dns": [{"servers": "${local.dns_resolvers}"}]}]}]}}})
}

test_a_resolver_written_as_a_value_is_refused if {
	denies(with_file({"locals": [{"dns_servers": ["9.9.9.9"]}]}), "names the resolver 9.9.9.9 directly")
}

test_a_resolver_written_beside_the_declaration_is_refused if {
	i := array.concat(filler, [{"path": "modules/c/variables.tf", "contents": {"locals": [{"dns_resolvers": ["1.1.1.1"], "fallback_dns_servers": ["9.9.9.9"]}]}}])
	denies(i, "names the resolver 9.9.9.9")
}

test_a_resolver_inside_a_rendered_configuration_is_refused if {
	denies(with_file({"data": {"cfg": {"a": [{"patch": "${yamlencode({\n  nameservers = [\"8.8.8.8\"]\n})}"}]}}}), "names the resolver 8.8.8.8")
}

test_a_comment_inside_an_expression_is_not_the_decision if {
	count(dns_resolvers.deny) == 0 with input as with_file({"data": {"cfg": {"a": [{"patch": "${yamlencode({\n  # the servers were 8.8.8.8 once\n  x = 1\n})}"}]}}})
}

test_an_address_with_nothing_to_do_with_resolving_passes if {
	count(dns_resolvers.deny) == 0 with input as with_file({"locals": [{"gateway": "10.0.0.1"}]})
}

test_two_declarations_are_refused if {
	denies(array.concat(with_file({}), [{"path": "modules/d/v.tf", "contents": {"locals": [{"dns_resolvers": ["1.1.1.1"]}]}}]), "declared 2 time(s)")
}
