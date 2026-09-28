package repo

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A concurrency group names the subject whose newer run replaces it.
//
// GitHub lets one run in a group proceed and cancels the rest: the pending one
// always, and the running one too when cancel-in-progress allows. So whatever
// can share a group can cancel it, and the group's key is the whole statement
// of what may replace what.
//
// Two keys here said less than they meant. Deploy Infrastructure keyed by
// github.ref, and a pull request closed by merging reports its BASE branch as
// github.ref - so the closing run joined main's group and cancelled the
// converge its own merge had just started (#573). The clerk keyed by
// github.event.pull_request.number, which a comment event does not have, so
// every comment anywhere shared one group and cancelled a reading in progress
// (#574).
//
// So, for every workflow and every job that declares a group:
//
//   - one triggered by a pull request keys by the pull request's number, never
//     by the ref, which is not the pull request's once it has merged;
//   - one triggered by a comment keys by the issue's number and by the
//     comment, since the comment decides whether it is a request at all;
//   - one triggered by a pull request and by anything else keys by the event
//     name too, so a pull request's run can never share a group with a push.
func TestEveryConcurrencyGroupNamesWhatSupersedesIt(t *testing.T) {
	examined := 0
	var findings []string
	for file, body := range workflowTexts(t) {
		f, n, err := auditConcurrency(file, []byte(body))
		if err != nil {
			t.Fatalf("%v", err)
		}
		findings = append(findings, f...)
		examined += n
	}
	if examined == 0 {
		t.Fatal("no concurrency group was found in any workflow, so this examined nothing")
	}
	sort.Strings(findings)
	for _, f := range findings {
		t.Error(f)
	}
}

// auditConcurrency returns what is wrong with one workflow's groups, and how
// many groups it examined.
func auditConcurrency(file string, body []byte) (findings []string, examined int, err error) {
	var wf struct {
		On          yaml.Node `yaml:"on"`
		Concurrency yaml.Node `yaml:"concurrency"`
		Jobs        map[string]struct {
			Concurrency yaml.Node `yaml:"concurrency"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(body, &wf); err != nil {
		return nil, 0, fmt.Errorf("parsing %s: %w", file, err)
	}
	triggers := triggerNames(&wf.On)
	pr := slices.Contains(triggers, "pull_request") || slices.Contains(triggers, "pull_request_target")
	comment := slices.Contains(triggers, "issue_comment")
	other := slices.ContainsFunc(triggers, func(s string) bool {
		return s != "pull_request" && s != "pull_request_target"
	})

	check := func(where string, n *yaml.Node) {
		group := concurrencyGroup(n)
		if group == "" {
			return
		}
		examined++
		need := func(ref, why string) {
			if !strings.Contains(group, ref) {
				findings = append(findings, fmt.Sprintf("%s: the concurrency group %q does not use %s, %s", where, group, ref, why))
			}
		}
		if pr {
			need("github.event.pull_request.number",
				"so it is not keyed by the pull request: a merged pull request reports its base branch as github.ref, and its closing run cancels whatever runs there (#573)")
		}
		if comment {
			need("github.event.issue.number",
				"so every comment shares one group: a comment event has no pull_request (#574)")
			need("github.event.comment",
				"so a comment that asks for nothing still replaces a run in progress (#574)")
		}
		if pr && other {
			need("github.event_name",
				"so a pull request's run can share a group with another event's and cancel it (#573)")
		}
	}
	check(file, &wf.Concurrency)
	for name, job := range wf.Jobs {
		check(file+" job "+name, &job.Concurrency)
	}
	return findings, examined, nil
}

// concurrencyGroup is a concurrency block's group: the scalar form is the
// group itself.
func concurrencyGroup(n *yaml.Node) string {
	switch n.Kind {
	case yaml.ScalarNode:
		return n.Value
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == "group" {
				return n.Content[i+1].Value
			}
		}
	}
	return ""
}

func TestAuditConcurrencyCatchesEachWayToShareAGroup(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"a push-only workflow may key by anything", `
on: {push: {branches: [main]}}
concurrency: {group: "deploy-${{ github.ref }}", cancel-in-progress: false}
`, ""},
		{"a pull request keyed by ref", `
on: {pull_request: {types: [closed]}}
concurrency: {group: "plan-${{ github.ref }}"}
`, "github.event.pull_request.number"},
		{"a pull request and a push sharing a key", `
on: {pull_request: {}, push: {}}
concurrency: {group: "d-${{ github.event.pull_request.number || github.ref }}"}
`, "github.event_name"},
		{"a comment keyed by the pull request", `
on: {pull_request: {}, issue_comment: {}}
concurrency: {group: "c-${{ github.event.pull_request.number }}-${{ github.event_name }}"}
`, "github.event.issue.number"},
		{"a comment that cannot tell a request from chatter", `
on: {issue_comment: {}}
concurrency: {group: "c-${{ github.event.issue.number }}"}
`, "github.event.comment"},
		{"the scalar form, on a job", `
on: {pull_request: {}}
jobs: {plan: {concurrency: "p-${{ github.ref }}"}}
`, "job plan"},
		{"everything named", `
on: {pull_request: {}, issue_comment: {}}
concurrency:
  group: >-
    c-${{ github.event.pull_request.number || github.event.issue.number }}-${{
    github.event_name == 'issue_comment' && github.event.comment.id || 'read' }}
`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			findings, examined, err := auditConcurrency("wf.yml", []byte(c.body))
			if err != nil {
				t.Fatal(err)
			}
			if examined != 1 {
				t.Fatalf("examined %d groups, want 1", examined)
			}
			got := strings.Join(findings, "\n")
			if c.want == "" && got != "" {
				t.Fatalf("want no finding, got:\n%s", got)
			}
			if c.want != "" && !strings.Contains(got, c.want) {
				t.Fatalf("want a finding naming %q, got:\n%s", c.want, got)
			}
		})
	}
}
