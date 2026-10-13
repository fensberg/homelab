# One group of the estate's own runners: which repositories may send work to
# them, and which workflows.
#
# A runner of the estate's is a machine inside it. A job that reaches one
# runs where the estate's network is, so who may ask for one is a wall and is
# set here, by the estate's own credential, and not left to what a workflow
# file says it wants.
terraform {
  required_providers {
    github = { source = "integrations/github" }
  }
}
