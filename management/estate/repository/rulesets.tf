# The rules on the repository's branches and tags: what may be pushed where,
# and what a change has to pass before it reaches the branch the estate is
# built from.
#
# Each was made by hand before any of this was. The estate takes the ones
# that stand as its own and does not make them again: the lawyer finds each
# by its name and adopts it, so their names are the ones they were given.
#
# NOT HERE YET, and still as it was made by hand: that a change is reviewed.
# One App may pass that by, and the estate's config does not yet say which;
# and one switch of that rule that is on today has no word in this provider.

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

locals {
  # The platform's own runner of workflows, as the platform numbers it: a
  # check of the same name reported by anything else does not count.
  the_platforms_workflow_runner = 15368

  required_checks = [
    "Secrets Scan (TruffleHog)",
    "Validate (tofu, kustomize, kubeconform, go)",
    "IaC Scan (Trivy)",
    "Logic Scan (Semgrep)",
    "Shell Lint (ShellCheck)",
    "Test (go, tofu, vitest)",
    "Sensitive Paths",
    "Policy Scan (Checkov)",
    "Dockerfile Scan (hadolint)",
    "Workflow Scan (zizmor)",
    "Spelling (codespell)",
    "Format (pre-commit)",
    "dependency-review",
  ]
}
