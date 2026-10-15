package harness

import (
	"regexp"

	"gopkg.in/yaml.v3"
)

// nightlyOnAPool is the one expression a job's runner may be written as: a
// pull-request pool when the workflow runs on its nightly schedule, and
// GitHub's runner otherwise. It is how a lane is tried on the estate's
// runners by night while pull requests go on using GitHub's by day
// (docs/epochs/08-agent-roles.md). Any other expression is a runner nothing
// here can name, and is read as one.
var nightlyOnAPool = regexp.MustCompile(`^\$\{\{\s*github\.event_name\s*==\s*'schedule'\s*&&\s*format\('(pr-[a-z]+)-\{0\}',\s*vars\.pull_request_runners_site\)\s*\|\|\s*'([A-Za-z0-9.-]+)'\s*\}\}$`)

// RunnersOf is every runner a job's runs-on can ask for, as a job asks for
// it: the runner it names, each runner a list names, or both runners of
// the nightly form.
func RunnersOf(n *yaml.Node) []string {
	switch n.Kind {
	case yaml.ScalarNode:
		if m := nightlyOnAPool.FindStringSubmatch(n.Value); m != nil {
			return []string{m[1] + "-${{ vars.pull_request_runners_site }}", m[2]}
		}
		return []string{n.Value}
	case yaml.SequenceNode:
		var out []string
		for _, c := range n.Content {
			out = append(out, c.Value)
		}
		return out
	}
	return nil
}

// OnAPoolByNight is whether a job's runs-on is the nightly form: on the
// estate's runners only when its workflow runs on its schedule, so that only
// those runs say how long it takes there.
func OnAPoolByNight(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && nightlyOnAPool.MatchString(n.Value)
}
