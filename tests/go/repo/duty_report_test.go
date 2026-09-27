package repo

import (
	"slices"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Every workflow that runs on a schedule says, once, when it is failing.
//
// A scheduled failure notifies only whoever last edited the cron, by email,
// and nothing else: the expediter failed daily for a week, and the nightly for
// four nights, before anyone knew (#441). Each scheduled workflow therefore
// ends with a `report` job that opens one issue on a failure and closes it on
// the next success.
//
// Asserted of every workflow with a schedule, discovered by reading them all,
// so a new one cannot start without it. The job must wait on every other job,
// or a failure in the one it forgot is the silent kind again; run only on the
// schedule, since a dispatched run has somebody watching it; and run the
// report itself.
func TestEveryScheduledWorkflowReportsItsFailures(t *testing.T) {
	scheduled := 0
	for file, body := range workflowTexts(t) {
		var wf struct {
			On   yaml.Node `yaml:"on"`
			Jobs map[string]struct {
				Needs yaml.Node `yaml:"needs"`
				If    string    `yaml:"if"`
				Steps []struct {
					Uses string            `yaml:"uses"`
					With map[string]string `yaml:"with"`
				} `yaml:"steps"`
			} `yaml:"jobs"`
		}
		if err := yaml.Unmarshal([]byte(body), &wf); err != nil {
			t.Fatalf("parsing %s: %v", file, err)
		}
		if !slices.Contains(triggerNames(&wf.On), "schedule") {
			continue
		}
		scheduled++

		report, ok := wf.Jobs["report"]
		if !ok {
			t.Errorf("%s runs on a schedule and has no report job, so its failures reach nobody (#441)", file)
			continue
		}
		var needs []string
		switch report.Needs.Kind {
		case yaml.ScalarNode:
			needs = []string{report.Needs.Value}
		case yaml.SequenceNode:
			for _, n := range report.Needs.Content {
				needs = append(needs, n.Value)
			}
		}
		var missing []string
		for name := range wf.Jobs {
			if name != "report" && !slices.Contains(needs, name) {
				missing = append(missing, name)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			t.Errorf("%s: the report job does not wait on %v, so a failure there is not reported", file, missing)
		}
		if !strings.Contains(report.If, "always()") || !strings.Contains(report.If, "github.event_name == 'schedule'") {
			t.Errorf("%s: the report job must run always() and only on the schedule; its if is %q", file, report.If)
		}
		runs := false
		for _, st := range report.Steps {
			if strings.HasSuffix(st.Uses, "/github-script") && st.With["name"] == "duty-report" {
				runs = true
			}
		}
		if !runs {
			t.Errorf("%s: the report job does not run duty-report", file)
		}
	}
	if scheduled == 0 {
		t.Fatal("no workflow runs on a schedule, so this checked nothing: the reader has stopped matching")
	}
}
