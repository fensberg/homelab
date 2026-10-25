# What Conftest asks: every rule's refusals, in one query.
#
# Each rule lives in a package under rules. Conftest asks one question per
# namespace, and hands each the whole input again, so forty namespaces cost
# forty conversions of a tree that is three megabytes of parsed YAML - about
# two of the four seconds a run took, before any rule had run. Asked once,
# here, every rule reads the same input. A refusal is prefixed with the rule's
# package, which is where to look for it.
#
# Nothing lists the rules. A package under rules is collected because it is
# there; one written anywhere else is refused by
# TestEveryConfigurationRuleProvesItself, so a rule cannot be skipped by being
# misnamed.
package main

import rego.v1

# METADATA
# description: Every rule's refusals, prefixed with the rule's package.
# entrypoint: true
deny contains $"{name}: {msg}" if {
	some name, rules in data.rules
	not endswith(name, "_test")
	some msg in rules.deny
}

# METADATA
# description: Every rule's warnings, prefixed with the rule's package.
# entrypoint: true
warn contains $"{name}: {msg}" if {
	some name, rules in data.rules
	not endswith(name, "_test")
	some msg in rules.warn
}
