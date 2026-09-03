//go:build api

package api_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// A required status check has to be one that can actually report.
//
// GitHub does not check this. A ruleset requires a check by name, and if
// nothing ever reports that name the pull request sits at "Expected — Waiting
// for status to be reported" indefinitely. There is no failure to read, no run
// to re-open, and nothing to re-run: the merge is blocked by the absence of an
// answer rather than by a bad one. It looks like strictness and it is a stuck
// gate.
//
// It happened. `Analyze (actions)` is CodeQL's workflow analysis, required by
// the "protected branches" ruleset - which covers `main` and `epoch/**` - while
// codeql.yml triggered only on pull requests into `main`. Every pull request
// into an epoch branch was unmergeable, and the visible symptom was so unlike a
// failure that the reasonable next thought was to relax the rules.
//
// So: for every ruleset, every check it requires must be produced by a job in
// some workflow that triggers on pull requests into the branches that ruleset
// covers.

type workflowFile struct {
	Name string    `yaml:"name"`
	On   yaml.Node `yaml:"on"`
	Jobs map[string]struct {
		Name     string `yaml:"name"`
		Strategy struct {
			Matrix map[string]yaml.Node `yaml:"matrix"`
		} `yaml:"strategy"`
	} `yaml:"jobs"`
}

// pullRequestBranches says whether a workflow runs on pull requests at all,
// and which base branches it filters to. `on:` takes three shapes - a scalar,
// a sequence of event names, and a mapping - and only the mapping can carry a
// filter, so the other two mean "every base branch".
func pullRequestBranches(on yaml.Node) (onPR bool, branches []string) {
	switch on.Kind {
	case yaml.ScalarNode:
		return on.Value == "pull_request", nil
	case yaml.SequenceNode:
		for _, n := range on.Content {
			if n.Value == "pull_request" {
				return true, nil
			}
		}
		return false, nil
	case yaml.MappingNode:
		for i := 0; i+1 < len(on.Content); i += 2 {
			if on.Content[i].Value != "pull_request" {
				continue
			}
			v := on.Content[i+1]
			if v.Kind != yaml.MappingNode {
				return true, nil // `pull_request:` with no filters
			}
			for j := 0; j+1 < len(v.Content); j += 2 {
				if v.Content[j].Value != "branches" {
					continue
				}
				var got []string
				if err := v.Content[j+1].Decode(&got); err == nil {
					return true, got
				}
			}
			return true, nil
		}
	}
	return false, nil
}

// jobNames returns every check name a workflow can report on a pull request,
// with a one-dimensional matrix expanded - which is how CodeQL produces
// "Analyze (actions)" from `name: "Analyze (${{ matrix.language }})"`.
func jobNames(w workflowFile) []string {
	var out []string
	for id, job := range w.Jobs {
		name := job.Name
		if name == "" {
			name = id
		}
		if !strings.Contains(name, "${{") {
			out = append(out, name)
			continue
		}
		expanded := false
		for key, node := range job.Strategy.Matrix {
			var values []string
			if err := node.Decode(&values); err != nil {
				continue
			}
			token := "${{ matrix." + key + " }}"
			if !strings.Contains(name, token) {
				continue
			}
			for _, v := range values {
				out = append(out, strings.ReplaceAll(name, token, v))
				expanded = true
			}
		}
		if !expanded {
			out = append(out, name)
		}
	}
	return out
}

// coversRef reports whether a `branches:` filter admits a ruleset's target.
// `~DEFAULT_BRANCH` is main here; `refs/heads/epoch/**` reduces to `epoch/**`.
// A workflow with no filter at all runs on every base branch.
func coversRef(filters []string, ref string) bool {
	if len(filters) == 0 {
		return true
	}
	target := strings.TrimPrefix(ref, "refs/heads/")
	if target == "~DEFAULT_BRANCH" || target == "~ALL" {
		target = "main"
	}
	for _, f := range filters {
		if f == target {
			return true
		}
		// `epoch/**` admits `epoch/**` and anything under it.
		if i := strings.Index(f, "**"); i >= 0 && strings.HasPrefix(target, f[:i]) {
			return true
		}
		if i := strings.Index(target, "**"); i >= 0 && strings.HasPrefix(f, target[:i]) {
			return true
		}
	}
	return false
}

func readWorkflows(t *testing.T) map[string]workflowFile {
	t.Helper()
	dir := filepath.Join("..", "..", "..", ".github", "workflows")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "reading .github/workflows")

	out := map[string]workflowFile{}
	for _, e := range entries {
		if e.IsDir() || (filepath.Ext(e.Name()) != ".yml" && filepath.Ext(e.Name()) != ".yaml") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		require.NoError(t, err)
		var w workflowFile
		require.NoErrorf(t, yaml.Unmarshal(body, &w), "parsing %s", e.Name())
		out[e.Name()] = w
	}
	require.NotEmpty(t, out, "no workflows were parsed, so this test checks nothing")
	return out
}

func TestEveryRequiredCheckCanReportOnTheBranchesItGates(t *testing.T) {
	workflows := readWorkflows(t)

	checked := 0
	for _, rs := range allRulesets(t) {
		d := detail(t, rs.ID)
		for _, r := range d.Rules {
			if r.Type != "required_status_checks" {
				continue
			}
			for _, ref := range d.Conditions.RefName.Include {
				for _, c := range r.Parameters.Checks {
					checked++

					var producedBy, wrongBranches []string
					for file, w := range workflows {
						onPR, filters := pullRequestBranches(w.On)
						for _, n := range jobNames(w) {
							if n != c.Context || !onPR {
								continue
							}
							if coversRef(filters, ref) {
								producedBy = append(producedBy, file)
							} else {
								wrongBranches = append(wrongBranches, file)
							}
						}
					}

					if len(producedBy) > 0 {
						continue
					}

					if len(wrongBranches) > 0 {
						t.Errorf("%q requires %q on %s, and %s produces that check but "+
							"does not run for pull requests into it.\n\n"+
							"Nothing will ever report the name, so every pull request "+
							"there waits at \"Expected — Waiting for status to be "+
							"reported\" with no failure to read and nothing to re-run.\n\n"+
							"Add the branch to that workflow's `on.pull_request.branches`, "+
							"or stop requiring the check on %s. Relaxing the ruleset to "+
							"force a merge past it treats a stuck gate as a strict one.",
							d.Name, c.Context, ref, strings.Join(wrongBranches, ", "), ref)
						continue
					}

					t.Errorf("%q requires %q on %s, and no workflow job produces a check "+
						"by that name at all.\n\n"+
						"Either it was renamed - a job's `name:` is the check name - or it "+
						"never existed. Every pull request into %s is unmergeable until "+
						"one or the other is corrected.",
						d.Name, c.Context, ref, ref)
				}
			}
		}
	}

	require.NotZero(t, checked,
		"no required check was examined, so this test passed without looking at anything")
}
