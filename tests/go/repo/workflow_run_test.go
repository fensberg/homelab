package repo

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A workflow started by another workflow takes nothing from it but its
// outcome.
//
// `workflow_run` runs the file as it is on the default branch, with the
// default branch's token, whatever started the run it watches - a pull
// request from a fork included. zizmor calls the trigger dangerous for that
// reason, and it is: the watched run's code, its artifacts and the names it
// chose are all an outsider's, arriving somewhere privileged.
//
// The one workflow here that uses it is excused, and this is why it may be.
// Any workflow with the trigger:
//
//   - reads only the watched run's name, conclusion, event and address, none
//     of which its author controls beyond choosing among workflows that exist;
//   - checks nothing out and fetches no artifact, so none of the watched run's
//     code or output is ever on the runner;
//   - holds no write permission but to issues.
func TestAWorkflowStartedByAnotherTakesNothingFromItButItsOutcome(t *testing.T) {
	allowed := map[string]bool{"name": true, "conclusion": true, "event": true, "html_url": true}
	field := regexp.MustCompile(`github\.event\.workflow_run\.([a-z_]+)`)
	fetches := regexp.MustCompile(`(?m)^\s*(?:- )?uses:\s*["']?\S*(checkout|download-artifact)\b`)

	var failures []string
	watchers := 0
	for _, rel := range trackedFiles(t) {
		if !runnable(rel) || strings.HasPrefix(rel[strings.LastIndex(rel, "/")+1:], "action.") {
			continue
		}
		raw := readRepoFile(t, rel)
		var w struct {
			On          yaml.Node            `yaml:"on"`
			Permissions yaml.Node            `yaml:"permissions"`
			Jobs        map[string]yaml.Node `yaml:"jobs"`
		}
		if err := yaml.Unmarshal([]byte(raw), &w); err != nil {
			t.Fatalf("%s does not parse: %v", rel, err)
		}
		if mappingValue(&w.On, "workflow_run") == nil {
			continue
		}
		watchers++
		body := stripYAMLComments(raw)

		for _, m := range field.FindAllStringSubmatch(body, -1) {
			if !allowed[m[1]] {
				failures = append(failures, rel+" reads the watched run's "+m[1]+", which whoever started that run may have chosen")
			}
		}
		if fetches.MatchString(body) {
			failures = append(failures, rel+" checks out or downloads an artifact, so the watched run's code or output reaches a privileged runner")
		}
		if !regexp.MustCompile(`(?m)^\s*checkout:\s*"false"\s*$`).MatchString(body) {
			failures = append(failures, rel+" does not tell its first step to check nothing out")
		}
		writes := func(where string, perms *yaml.Node) {
			if perms == nil || perms.Kind != yaml.MappingNode {
				failures = append(failures, rel+where+" does not spell out its permissions")
				return
			}
			for i := 0; i+1 < len(perms.Content); i += 2 {
				what, level := perms.Content[i].Value, perms.Content[i+1].Value
				if level != "read" && level != "none" && what != "issues" {
					failures = append(failures, rel+where+" may "+level+" "+what)
				}
			}
		}
		writes("", &w.Permissions)
		for name, job := range w.Jobs {
			if perms := mappingValue(&job, "permissions"); perms != nil {
				writes(", in its job "+name+",", perms)
			}
		}
	}
	if watchers == 0 {
		// Nothing uses the trigger, so nothing is excused for using it.
		return
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		t.Errorf("a workflow started by another workflow takes more from it than its outcome:\n\n  %s\n\n"+
			"It runs with the default branch's token whoever started the run it watches. Read "+
			"its name, conclusion, event and address and nothing else.", strings.Join(failures, "\n  "))
	}
}
