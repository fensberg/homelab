package repo

import (
	"testing"

	"gopkg.in/yaml.v3"

	"homelab/tests/harness"
)

// runnersOf is harness.RunnersOf: every runner a job's runs-on can ask for.
var runnersOf = harness.RunnersOf

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

// And a job is on a pool by night exactly when its runner is the nightly form.
func TestAJobIsOnAPoolByNightOnlyInTheNightlyForm(t *testing.T) {
	if !harness.OnAPoolByNight(&yaml.Node{Kind: yaml.ScalarNode, Value: "${{ github.event_name == 'schedule' && format('pr-builds-{0}', vars.pull_request_runners_site) || 'ubuntu-latest' }}"}) {
		t.Error("the nightly form is not read as on a pool by night")
	}
	for _, n := range []*yaml.Node{
		{Kind: yaml.ScalarNode, Value: "ubuntu-latest"},
		{Kind: yaml.ScalarNode, Value: pullRequestPool("builds")},
		{Kind: yaml.SequenceNode, Content: []*yaml.Node{{Kind: yaml.ScalarNode, Value: "ubuntu-latest"}}},
	} {
		if harness.OnAPoolByNight(n) {
			t.Errorf("%v is read as on a pool by night", n.Value)
		}
	}
}
