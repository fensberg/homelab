package repo

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A merge under a ruleset bypass merges exactly what was judged, in a job that
// judged it.
//
// `gh pr merge --admin` is how a bypass actor merges: without the flag gh
// refuses on its own side and never asks GitHub. The only bypass here is the
// procurement App's standing order, and the ruleset it bypasses holds the
// required checks as well as the review - so nothing GitHub runs bounds that
// merge. The expedite duty's first merge under the order was refused, and
// reading why showed the release job would have taken any pull request on an
// expedite/ branch, by any author, judged by a check that had run under a
// different holder login.
//
// So every --admin merge must carry --match-head-commit, so a push after the
// verdict cannot ride along, and its job must run enforce-standing-order
// itself.
var continuation = regexp.MustCompile(`\\\n\s*`)

type bypassWorkflow struct {
	Jobs map[string]struct {
		Steps []struct {
			Run string `yaml:"run"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func TestEveryBypassMergeIsJudgedWhereItMerges(t *testing.T) {
	workflows := workflowTexts(t)
	names := make([]string, 0, len(workflows))
	for name := range workflows {
		names = append(names, name)
	}
	sort.Strings(names)

	found := 0
	for _, name := range names {
		var wf bypassWorkflow
		if err := yaml.Unmarshal([]byte(workflows[name]), &wf); err != nil {
			t.Fatalf("%s does not parse: %v", name, err)
		}
		for job, j := range wf.Jobs {
			judged := false
			var merges []string
			for _, s := range j.Steps {
				script := continuation.ReplaceAllString(s.Run, " ")
				if strings.Contains(script, "enforce-standing-order") {
					judged = true
				}
				for _, line := range strings.Split(script, "\n") {
					if strings.Contains(line, "gh pr merge") && strings.Contains(line, "--admin") {
						merges = append(merges, strings.TrimSpace(line))
					}
				}
			}
			for _, m := range merges {
				found++
				if !strings.Contains(m, "--match-head-commit") {
					t.Errorf("%s job %s merges under a bypass without --match-head-commit:\n  %s\n\n"+
						"A bypass skips review and required checks alike, so the commit judged must be the "+
						"commit merged. Without the flag, anything pushed after the verdict merges with it.",
						name, job, m)
				}
				if !judged {
					t.Errorf("%s job %s merges under a bypass and never runs enforce-standing-order.\n\n"+
						"The Sensitive Paths check cannot bound this merge: the bypass skips it. The job "+
						"that merges must judge the standing order itself, against the head it merges.",
						name, job)
				}
			}
		}
	}
	// A floor: the expedite release merges under the bypass, so finding none
	// means the pattern has stopped matching rather than that nothing does.
	if found == 0 {
		t.Error("no workflow merges with `gh pr merge --admin`, so this checked nothing - " +
			"either the expedite release stopped using the bypass or the pattern stopped matching it")
	}
}
