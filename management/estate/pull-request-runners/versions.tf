# The group the pools of pull-request runners join.
#
# A group of its own, because the estate's other runners are in one that
# takes only named workflows as they are on the default branch, and a pull
# request never runs from the default branch: its workflow is its own copy.
# So this group cannot be held to workflows at all. It is held to the
# estate's repositories, and what holds a pull request's job is not the
# group: it is the machine the pools run on, which is closed to the rest of
# the estate, and each pool's own policy (clusters/core).
#
# tests/go/repo allows a group that takes any workflow only when it is the
# one the pull-request pools join, and nothing else joins it.
terraform {
  required_providers {
    github = { source = "integrations/github" }
  }
}
