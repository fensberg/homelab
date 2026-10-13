// Package ghapi builds GitHub REST API addresses.
//
// Written once: the security patrol and the sweeper each carried the same
// endpoint builder.
package ghapi

import "fmt"

// DefaultBase is GitHub's REST API.
const DefaultBase = "https://api.github.com"

// URL is the API address for a path, formatted like fmt.Sprintf, under base -
// or under DefaultBase when base is empty, which is how a test points a client
// at a local server and production leaves it alone.
func URL(base, format string, args ...any) string {
	if base == "" {
		base = DefaultBase
	}
	return base + fmt.Sprintf(format, args...)
}

// RulesetType is the OpenTofu resource every rule on a repository's branches
// and tags is. The lawyer adopts the ones that stand by it, and the guard on
// what belongs to the estate names it.
const RulesetType = "github_repository_ruleset"

// OrganisationSettingsType is the OpenTofu resource an organisation's own
// settings are: one for the organisation, adopted by the lawyer and named by
// the guard on what belongs to the estate.
const OrganisationSettingsType = "github_organization_settings"

// RunnerGroupType is the OpenTofu resource a group of self-hosted runners
// is: adopted by the lawyer, named by the guard on what belongs to the
// estate, and read by the guard on which workflows a group admits.
const RunnerGroupType = "github_actions_runner_group"

// ConvergeWorkflow is the workflow file whose runs on main are the converge
// of a merge. The contractor asks whether one is queued before it breaks
// ground, and security's patrol asks whether the last one failed; both ask
// about the workflow by this file, because a run's own name is whatever the
// workflow chose to call it.
const ConvergeWorkflow = "deploy-infrastructure.yml"
