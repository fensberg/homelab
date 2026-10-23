package repo

import (
	"regexp"

	"gopkg.in/yaml.v3"
)

// The pools a pull request's job may use, and the readings of a workflow's
// runners and triggers the guards share. That no pull request's job runs on
// the estate's runner is a configuration rule now
// (policy/conftest/pull_request_runners.rego).

// pullRequestPools are self-hosted pools built for pull-request work: no
// secret, a machine of their own in an untrusted zone, and egress that
// cannot reach the estate. Each is declared here by the name a job asks for
// it by, and not before its network policy is:
// TestEachPoolOfPullRequestRunnersReachesWhatItsJobsDoAndNoMore holds every
// pool to having one, and to these being the pools there are.
//
// The site is the estate's to say and is read from the repository's
// variable, so a pool's name ends in that and never in a site written out.
var pullRequestPools = map[string]bool{
	pullRequestPool("source"):   true,
	pullRequestPool("builds"):   true,
	pullRequestPool("packages"): true,
}

// pullRequestPool is the name a job asks for one of the pools by.
func pullRequestPool(pool string) string {
	return "pr-" + pool + "-${{ vars.pull_request_runners_site }}"
}

// hostedLabel is a runner GitHub provides.
var hostedLabel = regexp.MustCompile(`^(ubuntu|windows|macos)-[a-z0-9.-]+$`)

type workflowFile struct {
	On   yaml.Node              `yaml:"on"`
	Jobs map[string]workflowJob `yaml:"jobs"`
}

type workflowJob struct {
	RunsOn      yaml.Node `yaml:"runs-on"`
	If          string    `yaml:"if"`
	Environment yaml.Node `yaml:"environment"`
}

// triggerNames flattens the three shapes `on:` is allowed to take - a scalar,
// a sequence of event names, or a mapping of event name to filters.
func triggerNames(n *yaml.Node) []string {
	var out []string
	switch n.Kind {
	case yaml.ScalarNode:
		out = append(out, n.Value)
	case yaml.SequenceNode:
		for _, c := range n.Content {
			out = append(out, c.Value)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			out = append(out, n.Content[i].Value)
		}
	}
	return out
}

// --- the rules themselves ---------------------------------------------------
