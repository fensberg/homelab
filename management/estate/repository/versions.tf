# One repository's walls: who may send it work, and what that work may do.
#
# An estate is every repository under one organisation, so this is called
# once for each, from source-control.tf, and each stands behind the same
# walls. Set with the estate's own credential and no other (see the provider
# in source-control.tf): whoever could set these could take them down.
#
# Each wall goes in first AS IT STANDS, so that the first time it is set
# nothing moves by accident, and is tightened after, on its own, where a
# plan shows that one change and nothing else.
terraform {
  required_providers {
    github = { source = "integrations/github" }
  }
}
