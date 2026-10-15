package repo

import (
	"regexp"
	"testing"

	"gopkg.in/yaml.v3"
)

// nightlyOnAPool is the one expression a job's runner may be written as: a
// pull-request pool when the workflow runs on its nightly schedule, and
// GitHub's runner otherwise. It is how a lane is tried on the estate's
// runners by night while pull requests go on using GitHub's by day
// (docs/epochs/08-agent-roles.md). Any other expression is a runner nothing
// here can name, and is read as one.
var nightlyOnAPool = regexp.MustCompile(`^\$\{\{\s*github\.event_name\s*==\s*'schedule'\s*&&\s*format\('(pr-[a-z]+)-\{0\}',\s*vars\.pull_request_runners_site\)\s*\|\|\s*'([A-Za-z0-9.-]+)'\s*\}\}$`)

// runnersOf is every runner a job's runs-on can ask for, as a job asks for
// it: the runner it names, each runner a list names, or both runners of
// the nightly form.
func runnersOf(n *yaml.Node) []string {
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

// The nightly form is read as both its runners, and nothing that only
// resembles it is: another event, another variable, another kind of name
// on either side is a runner nothing here can say anything about.
func TestTheNightlyFormIsReadAsBothItsRunnersAndNothingElseIsReadAsIt(t *testing.T) {
	read := func(value string) []string {
		return runnersOf(&yaml.Node{Kind: yaml.ScalarNode, Value: value})
	}
	got := read("${{ github.event_name == 'schedule' && format('pr-builds-{0}', vars.pull_request_runners_site) || 'ubuntu-latest' }}")
	if len(got) != 2 || got[0] != pullRequestPool("builds") || got[1] != "ubuntu-latest" {
		t.Errorf("the nightly form was read as %v", got)
	}
	for _, near := range []string{
		"${{ github.event_name == 'push' && format('pr-builds-{0}', vars.pull_request_runners_site) || 'ubuntu-latest' }}",
		"${{ github.event_name == 'schedule' && format('pr-builds-{0}', inputs.site) || 'ubuntu-latest' }}",
		"${{ github.event_name == 'schedule' && format('homelab-{0}', vars.pull_request_runners_site) || 'ubuntu-latest' }}",
		"${{ github.event_name == 'schedule' && format('pr-builds-{0}', vars.pull_request_runners_site) || inputs.runner }}",
	} {
		if got := read(near); len(got) != 1 || got[0] != near {
			t.Errorf("%s was read as %v, as if it were the nightly form", near, got)
		}
	}
}
