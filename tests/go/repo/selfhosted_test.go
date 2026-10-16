package repo

import (
	"homelab/details/repopath"
	"os"
	"path/filepath"
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

// scaleSetName reads the runner scale set's name out of the manifest that
// declares it, rather than hard-coding it here.
//
// The guard above decides whether a job reaches the estate's own runner, and
// it used to compare against the literal "self-hosted". Renaming the scale set
// - which had to happen, because self-hosted is a reserved label that scale
// sets are never offered jobs for - would have left this matching a string
// nothing uses, so every job would have looked hermetic and the guard would
// have protected nothing while still passing.
func scaleSetName() string {
	// Found by the release it declares, wherever the manifest is. No name is
	// an answer the callers refuse: a guard that could not read the name
	// would otherwise compare every job against nothing and pass.
	path, err := fluxObjectPath(kindHelmRelease, runnerScaleSet)
	if err != nil {
		return ""
	}
	root, err := repopath.Root()
	if err != nil {
		return ""
	}
	body, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		return ""
	}
	if m := scaleSetNamePattern.FindStringSubmatch(string(body)); m != nil {
		return m[1]
	}
	return ""
}

var scaleSetNamePattern = regexp.MustCompile(`(?m)^\s*runnerScaleSetName:\s*(\S+)\s*$`)
