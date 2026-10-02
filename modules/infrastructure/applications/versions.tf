# What every application declares about itself, read once for OpenTofu.
#
# An application is one directory under modules/applications/, and what the
# estate's general mechanisms need to know about it is in application.json in
# that directory (scripts/details/applications reads the same file for every
# program). This module is the reading of it for the roots: the estate routes
# each declared address through a site's tunnel, the address plan turns each
# into the site's own address, and the platform creates each declared Secret.
# None of them names an application. They take whatever is declared.
#
# Pure, like the address plan: no provider, no resource, no state.
terraform {
  required_version = ">= 1.9.0"
}
