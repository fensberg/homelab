# The estate's address plan: every range, address, machine identity and name,
# computed once, for every site at once (docs/epochs/02-abstraction.md, "The
# addressing scheme is computed once").
#
# Pure: no provider, no resource, no state. That is what lets three different
# readers ask it the same question and get the same answer - the cluster root
# calls it as a module, and the contractor and the test harness ask it through
# `tofu console`, offline, with no credentials and no init.
#
# It holds no access rules. Who may reach what is epoch 09's, and refers to the
# names this produces.
terraform {
  required_version = ">= 1.9.0"
}
