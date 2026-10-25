package repo

import (
	"testing"

	"gopkg.in/yaml.v3"

	"homelab/tests/harness"
)

// runnersOf is harness.RunnersOf: every runner a job's runs-on can ask for.
var runnersOf = harness.RunnersOf

// A runner is read as it is written: one name, or each name of a list. An
// expression - the form that once sent a lane to a pool by night and to
// GitHub's runner by day - is read as itself, a runner nothing here can say
// anything about, and so one the rules refuse.
func TestARunnerIsReadAsItIsWrittenAndAnExpressionIsNoRunnerItCanName(t *testing.T) {
	t.Parallel()
	read := func(value string) []string {
		return runnersOf(&yaml.Node{Kind: yaml.ScalarNode, Value: value})
	}
	if got := read(pullRequestPool("builds")); len(got) != 1 || got[0] != pullRequestPool("builds") {
		t.Errorf("a pool was read as %v", got)
	}
	expression := "${{ github.event_name == 'schedule' && format('pr-builds-{0}', vars.pull_request_runners_site) || 'ubuntu-latest' }}"
	if got := read(expression); len(got) != 1 || got[0] != expression {
		t.Errorf("an expression was read as %v, as if it named its runners", got)
	}
	list := &yaml.Node{Kind: yaml.SequenceNode, Content: []*yaml.Node{{Kind: yaml.ScalarNode, Value: "self-hosted"}, {Kind: yaml.ScalarNode, Value: "linux"}}}
	if got := runnersOf(list); len(got) != 2 || got[0] != "self-hosted" || got[1] != "linux" {
		t.Errorf("a list was read as %v", got)
	}
}
