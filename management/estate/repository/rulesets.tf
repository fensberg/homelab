# The rules on the repository's branches and tags: what may be pushed where,
# and what a change has to pass before it reaches the branch the estate is
# built from.
#
# Each was made by hand before any of this was. The estate takes the ones
# that stand as its own and does not make them again: the lawyer finds each
# by its name and adopts it, so their names are the ones they were given.
#

# A release, once tagged, is that commit for good: the tag cannot be moved,
# rewritten or taken away, by anybody.
resource "github_repository_ruleset" "releases" {
  repository  = var.name
  name        = "Releases"
  target      = "tag"
  enforcement = "active"

  conditions {
    ref_name {
      include = ["refs/tags/v*"]
      exclude = []
    }
  }

  rules {
    deletion         = true
    non_fast_forward = true
    update           = true
  }
}

# On every branch: history is not rewritten, and every commit is signed.
resource "github_repository_ruleset" "all_branches" {
  repository  = var.name
  name        = "all branches"
  target      = "branch"
  enforcement = "active"

  conditions {
    ref_name {
      include = ["~ALL"]
      exclude = []
    }
  }

  rules {
    non_fast_forward    = true
    required_signatures = true
  }
}

# On the branch the estate is built from: one line of history, and every
# change waits its turn in the queue and is checked as it will land.
resource "github_repository_ruleset" "default_branch" {
  repository  = var.name
  name        = "main only"
  target      = "branch"
  enforcement = "active"

  conditions {
    ref_name {
      include = ["~DEFAULT_BRANCH"]
      exclude = []
    }
  }

  rules {
    required_linear_history = true

    merge_queue {
      merge_method                      = "SQUASH"
      grouping_strategy                 = "ALLGREEN"
      max_entries_to_build              = 5
      min_entries_to_merge              = 1
      max_entries_to_merge              = 5
      min_entries_to_merge_wait_minutes = 5
      check_response_timeout_minutes    = 60
    }
  }
}

# On the branch the estate is built from and on an epoch's: it cannot be
# deleted, and a change reaches it only past every check named here, each
# reported by the platform's own runner of workflows and by nothing else.
#
# NOBODY PASSES THEM BY. One person could, always, when this was made by
# hand. A check that is broken is mended; it is not walked around.
resource "github_repository_ruleset" "checks" {
  repository  = var.name
  name        = "protected branches - checks"
  target      = "branch"
  enforcement = "active"

  conditions {
    ref_name {
      include = ["~DEFAULT_BRANCH", "refs/heads/epoch/**"]
      exclude = []
    }
  }

  rules {
    deletion = true

    required_status_checks {
      strict_required_status_checks_policy = false
      do_not_enforce_on_create             = true

      dynamic "required_check" {
        for_each = local.required_checks
        content {
          context        = required_check.value
          integration_id = local.the_platforms_workflow_runner
        }
      }
    }
  }
}

# On the same branches: a change is read and approved by somebody who owns
# what it touches, an approval does not outlive a later push, and every
# thread on it is settled first.
#
# ONE PARTY PASSES THIS BY: the App that brings a pin or a delivery, which
# nobody wrote and whose content was reviewed where it was made. It passes
# the review and not the checks.
#
# ONE SWITCH OF THIS RULE IS NOT WRITTEN HERE, because this provider has no
# word for it: an extra approval for a change nobody is attributed with. It
# is on, set by hand. While a plan shows no change to this rule nothing is
# sent and it stays on; a change to anything here sends the whole rule, and
# that switch has to be looked at afterwards.
resource "github_repository_ruleset" "review" {
  repository  = var.name
  name        = "protected branches - review"
  target      = "branch"
  enforcement = "active"

  conditions {
    ref_name {
      include = ["~DEFAULT_BRANCH", "refs/heads/epoch/**"]
      exclude = []
    }
  }

  bypass_actors {
    actor_type  = "Integration"
    actor_id    = var.delivers_unreviewed
    bypass_mode = "always"
  }

  rules {
    pull_request {
      required_approving_review_count   = 1
      dismiss_stale_reviews_on_push     = true
      require_code_owner_review         = true
      require_last_push_approval        = false
      required_review_thread_resolution = true
      allowed_merge_methods             = ["squash", "rebase"]
    }
  }
}

locals {
  # The platform's own runner of workflows, as the platform numbers it: a
  # check of the same name reported by anything else does not count.
  the_platforms_workflow_runner = 15368

  required_checks = [
    # Every lane of pr-validation.yml, by way of its gate, so a lane can change
    # without this list changing. A check here cannot be renamed: the pull
    # request doing it would be held to a name it no longer reports.
    "tests passed",
    "Sensitive Paths",
    "dependency-review",
    # The plan: whether the site's hypervisor has room for what a change
    # asks and whether the site can hold it. It answers for every change,
    # passing one that touches nothing a site is built from, which is what
    # lets it be required. Before it was, a change the plan would have
    # refused was planned, approved and merged, and refused by the converge
    # with main left declaring what could not be built.
    "Plan",
  ]
}
