package repo

import (
	"slices"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Every workflow that runs on a schedule is watched, once, for failing.
//
// A scheduled failure notifies only whoever last edited the cron, by email,
// and nothing else: the expediter failed daily for a week, and the nightly for
// four nights, before anyone knew (#441). duty.yml watches every scheduled
// workflow finish and keeps one issue open per workflow while it fails.
//
// It used to be a `report` job in each scheduled workflow. That job was skipped
// on every run that was not scheduled, so a pull request's CodeQL run listed a
// skipped Report among its checks, and it was the same job copied six times.
//
// duty.yml names the workflows it watches, by name, because that is what
// workflow_run accepts - so both directions are asserted: a scheduled workflow
// it does not name is one whose failures reach nobody, and a name that matches
// no scheduled workflow is a rename that quietly stopped the watch. And the
// watcher reports only scheduled runs, and runs the report itself.
func TestEveryScheduledWorkflowIsWatchedForFailing(t *testing.T) {
	type workflow struct {
		Name string    `yaml:"name"`
		On   yaml.Node `yaml:"on"`
		Jobs map[string]struct {
			If    string `yaml:"if"`
			Steps []struct {
				Uses string            `yaml:"uses"`
				With map[string]string `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}

	scheduled := map[string]string{} // name -> file
	var duty workflow
	for file, body := range workflowTexts(t) {
		var wf workflow
		if err := yaml.Unmarshal([]byte(body), &wf); err != nil {
			t.Fatalf("parsing %s: %v", file, err)
		}
		if file == "duty.yml" {
			duty = wf
		}
		if slices.Contains(triggerNames(&wf.On), "schedule") {
			scheduled[wf.Name] = file
		}
	}
	if len(scheduled) == 0 {
		t.Fatal("no workflow runs on a schedule, so this checked nothing: the reader has stopped matching")
	}
	if duty.Name == "" {
		t.Fatal("duty.yml is missing, so no scheduled failure reaches anybody (#441)")
	}

	var watched []string
	var on struct {
		WorkflowRun struct {
			Workflows []string `yaml:"workflows"`
		} `yaml:"workflow_run"`
	}
	if err := duty.On.Decode(&on); err != nil {
		t.Fatalf("reading duty.yml's trigger: %v", err)
	}
	watched = on.WorkflowRun.Workflows

	var unwatched []string
	for name, file := range scheduled {
		if !slices.Contains(watched, name) {
			unwatched = append(unwatched, name+" ("+file+")")
		}
	}
	sort.Strings(unwatched)
	for _, u := range unwatched {
		t.Errorf("%s runs on a schedule and duty.yml does not watch it, so its failures are not reported (#441). Add its name to duty.yml's workflow_run.workflows.", u)
	}
	for _, name := range watched {
		if _, ok := scheduled[name]; !ok {
			t.Errorf("duty.yml watches %q, and no scheduled workflow is called that. A renamed workflow drops out of the watch without a word; name it as it is now.", name)
		}
	}

	reports := false
	for _, job := range duty.Jobs {
		for _, st := range job.Steps {
			if strings.HasSuffix(st.Uses, "/github-script") && st.With["name"] == "duty-report" {
				reports = true
				if !strings.Contains(job.If, "github.event.workflow_run.event == 'schedule'") {
					t.Errorf("duty.yml reports runs that were not scheduled; its if is %q. A dispatched run has somebody watching it.", job.If)
				}
			}
		}
	}
	if !reports {
		t.Error("duty.yml does not run duty-report, so it watches and says nothing")
	}
}
