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

// The scripts beside $/.github/workflows/github-script, and who runs them.
//
// Callers name a script rather than pass code in, so that template-injection
// analysis can still read it. That turns a script into a file and a caller into
// a name, and each half can outlive the other without anything failing until a
// job runs: a step naming a script that is not there fails only on the
// infrastructure change that reaches it, and a script nobody names is code
// sitting beside a token with no reason to exist.

const sharedScriptDir = ".github/workflows/github-script/"

var scriptName = regexp.MustCompile(`^[a-z0-9-]+$`)

// sharedScripts returns every script beside the shared action, by name, as the
// tree will be once outstanding patches land.
func sharedScripts(t *testing.T) map[string]string {
	t.Helper()
	root := intendedRoot(t)
	out := map[string]string{}
	for _, rel := range trackedFilesIn(t, root) {
		if !strings.HasPrefix(rel, sharedScriptDir) || !strings.HasSuffix(rel, ".js") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		out[strings.TrimSuffix(strings.TrimPrefix(rel, sharedScriptDir), ".js")] = string(body)
	}
	return out
}

// namedScripts returns every script a workflow or composite action runs through
// the shared action, with where it is named.
func namedScripts(t *testing.T) map[string][]string {
	t.Helper()
	root := intendedRoot(t)
	named := map[string][]string{}
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
		steps := doc.Runs.Steps
		for _, j := range doc.Jobs {
			steps = append(steps, j.Steps...)
		}
		for _, step := range steps {
			if uses, _ := step["uses"].(string); uses != "$/.github/workflows/github-script" {
				continue
			}
			with, _ := step["with"].(map[string]any)
			name, _ := with["name"].(string)
			named[name] = append(named[name], rel)
		}
	}
	return named
}

func TestEverySharedScriptIsNamedAndEveryNameIsAScript(t *testing.T) {
	scripts := sharedScripts(t)
	named := namedScripts(t)

	if len(named) == 0 {
		t.Fatal("no step runs a shared script, and the plan comments do - the read has stopped matching")
	}

	var names []string
	for n := range named {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		where := strings.Join(named[n], ", ")
		if !scriptName.MatchString(n) {
			t.Errorf(`%s names the shared script %q, which is not a script name.

The name becomes part of a path, so the action refuses anything but lower-case
letters, digits and hyphens - and the job would find that out only when it ran.`, where, n)
			continue
		}
		if _, ok := scripts[n]; !ok {
			t.Errorf(`%s runs the shared script %q, and there is no %s%s.js.

The step fails only when its job runs, and the plan comments run only on
infrastructure changes - so this would surface on the one pull request where a
reviewer needed the comment.`, where, n, sharedScriptDir, n)
		}
	}

	var files []string
	for n := range scripts {
		files = append(files, n)
	}
	sort.Strings(files)
	for _, n := range files {
		if _, ok := named[n]; !ok {
			t.Errorf(`%s%s.js is not run by any step.

A script beside the shared action runs with a job's token whenever something
names it. One that nothing names is code with that reach and no reason to exist,
and the next step to name it inherits whatever it does without anybody reading
it for that purpose. Remove it.`, sharedScriptDir, n)
		}
	}
}
