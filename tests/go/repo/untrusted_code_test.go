package repo

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A run holding secrets never executes code it did not start with.
//
// WHAT WAS WRONG (#409). clerk.yml runs on issue_comment, which executes the
// workflow from the default branch WITH repository secrets, whoever wrote the
// pull request being discussed. It checked the pull request's head out as the
// workspace, ran ./.github/actions/versions and `go build -C scripts/clerk`
// from it, and handed the result the clerk App's private key. Its `if:`
// required the COMMENTER to be a collaborator, which says nothing about who
// wrote the code. An owner typing `@clerk snag` on a stranger's fork ran the
// stranger's code holding a credential that mints tokens for this repository.
//
// WHY IT WAS INVISIBLE. CodeQL raised nothing while the checkout sat in the
// workflow, apparently accepting the association check as a guard. It raised
// it only once checkout moved into a shared action taking a `ref` input -
// where it could no longer see any caller's condition at all.
//
// Two rules, because the hole had two halves: a checkout that could be pointed
// anywhere, and a privileged run that put another commit where things execute.

// The only thing a checkout may be told is how much history to fetch.
//
// An allow list rather than refusing `ref` and `repository` by name: any input
// that changes WHAT is checked out, including one checkout grows later, is the
// same hole. With the commit fixed to the one the run is for, code from
// elsewhere can only arrive through an explicit fetch - which the next rule
// watches.
func TestNoCheckoutCanBePointedAtOtherCode(t *testing.T) {
	allowed := map[string]bool{"fetch-depth": true, "persist-credentials": true}

	checkouts := 0
	forEachRunnableStep(t, func(file string, step map[string]any) {
		uses, _ := step["uses"].(string)
		if !strings.Contains(uses, "actions/checkout@") {
			return
		}
		checkouts++
		with, _ := step["with"].(map[string]any)
		var keys []string
		for k := range with {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if !allowed[k] {
				t.Errorf(`%s gives actions/checkout %q.

A checkout here may only be told how deep to fetch. Anything that changes WHAT
is checked out lets a caller place code that was never reviewed where later
steps execute - which, in a run holding secrets, is how the clerk handed a
stranger's pull request its App's private key (#409).

To read other code, fetch it as data into its own worktree and do not run it,
as clerk.yml does.`, file, k)
			}
		}
	})
	if checkouts == 0 {
		t.Fatal("no actions/checkout step was found in any workflow or action, so this checked nothing")
	}
}

// A privileged run keeps another commit out of the workspace.
//
// issue_comment, pull_request_target and workflow_run run with secrets whatever
// the pull request is. In those, a pull request's code may be fetched only into
// its own worktree, and nothing may move the workspace onto it.
//
// WHAT THIS CANNOT CATCH, stated so nobody reads it as more. It reads `run:`
// text. It refuses the git commands that replace the workspace's contents by
// name, so a way of doing that it does not name - an archive piped into tar, a
// binary fetched and run - passes. And it cannot see whether a step later runs
// something from inside the worktree. The rule above is the structural half;
// this is the tripwire beside it.
var (
	privilegedTrigger = map[string]bool{
		"issue_comment":       true,
		"pull_request_target": true,
		"workflow_run":        true,
	}
	pullRequestCode = regexp.MustCompile(`refs/pull/|pull_request\.head`)
	movesWorkspace  = regexp.MustCompile(`\bgit\b[^\n;&|]*\s(checkout|switch|reset|restore|pull|merge|read-tree|stash\s+apply)\b`)
)

func TestNoPrivilegedRunPutsAPullRequestInTheWorkspace(t *testing.T) {
	root := repoRoot(t)

	privileged := 0
	for _, rel := range trackedFilesIn(t, root) {
		if !authoredHere(rel) || !runnable(rel) || filepath.Base(rel) == "action.yml" {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		var doc struct {
			On   yaml.Node `yaml:"on"`
			Jobs map[string]struct {
				Steps []map[string]any `yaml:"steps"`
			} `yaml:"jobs"`
		}
		if err := yaml.Unmarshal(body, &doc); err != nil {
			t.Fatalf("parsing %s: %v", rel, err)
		}
		if !hasPrivilegedTrigger(&doc.On) {
			continue
		}
		privileged++

		var jobs []string
		for j := range doc.Jobs {
			jobs = append(jobs, j)
		}
		sort.Strings(jobs)
		for _, j := range jobs {
			for _, step := range doc.Jobs[j].Steps {
				run, _ := step["run"].(string)
				if run == "" {
					continue
				}
				name, _ := step["name"].(string)
				if m := movesWorkspace.FindString(run); m != "" {
					t.Errorf(`%s job %q step %q runs %q.

This workflow runs with secrets on a trigger that anybody's pull request can
reach, and that command replaces what is in the workspace - where every later
step, and every ./ action, executes from. A pull request's code belongs in its
own worktree, read and never run.`, rel, j, name, strings.TrimSpace(m))
				}
				if pullRequestCode.MatchString(run) && !strings.Contains(run, "git worktree add") {
					t.Errorf(`%s job %q step %q fetches a pull request's code without its own worktree.

In a run holding secrets, a pull request is data. Fetch it into a worktree of
its own - `+"`git worktree add --detach <dir> <commit>`"+` - so nothing that
executes from the workspace can be reached by it.`, rel, j, name)
				}
			}
		}
	}

	if privileged == 0 {
		t.Fatal("no workflow runs on a privileged trigger, and clerk.yml does - the read has stopped matching")
	}
}

func hasPrivilegedTrigger(on *yaml.Node) bool {
	switch on.Kind {
	case yaml.ScalarNode:
		return privilegedTrigger[on.Value]
	case yaml.SequenceNode:
		for _, n := range on.Content {
			if privilegedTrigger[n.Value] {
				return true
			}
		}
	case yaml.MappingNode:
		for i := 0; i < len(on.Content); i += 2 {
			if privilegedTrigger[on.Content[i].Value] {
				return true
			}
		}
	}
	return false
}

// forEachRunnableStep visits every step GitHub executes, in workflows and in
// composite actions.
func forEachRunnableStep(t *testing.T, fn func(file string, step map[string]any)) {
	t.Helper()
	root := repoRoot(t)
	for _, rel := range trackedFilesIn(t, root) {
		if !authoredHere(rel) || !runnable(rel) {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		var doc struct {
			Jobs map[string]struct {
				Steps []map[string]any `yaml:"steps"`
			} `yaml:"jobs"`
			Runs struct {
				Steps []map[string]any `yaml:"steps"`
			} `yaml:"runs"`
		}
		if err := yaml.Unmarshal(body, &doc); err != nil {
			t.Fatalf("parsing %s: %v", rel, err)
		}
		for _, s := range doc.Runs.Steps {
			fn(rel, s)
		}
		for _, j := range doc.Jobs {
			for _, s := range j.Steps {
				fn(rel, s)
			}
		}
	}
}
