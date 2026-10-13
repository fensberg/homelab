package repo

import (
	"sort"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"gopkg.in/yaml.v3"
)

// Every check a merge is held to is the name of a job some workflow runs.
//
// WHY. The estate sets which checks a change has to pass, by name. The
// platform waits for a check of that name and has no way to know nobody
// will ever report one: a name mistyped, or a job renamed in a workflow and
// not here, holds every pull request for ever. Nobody may pass the checks
// by, so the way out of that is converging the estate by hand.
//
// FOUND BY WHAT IT IS. The list is whatever is declared as the checks
// required, wherever it is, and the names are every job of every workflow.
const requiredChecksDeclared = "required_checks"

// requiredChecksIn is every check named where the checks a merge is held to
// are declared.
func requiredChecksIn(sources map[string]string) (checks []string, declared int, err error) {
	for path, body := range sources {
		file, diags := hclsyntax.ParseConfig([]byte(body), path, hcl.InitialPos)
		if diags.HasErrors() {
			return nil, 0, diags
		}
		for _, block := range file.Body.(*hclsyntax.Body).Blocks {
			if block.Type != "locals" {
				continue
			}
			list := written(block.Body, requiredChecksDeclared)
			if list.IsNull() || !list.CanIterateElements() {
				continue
			}
			declared++
			for _, v := range list.AsValueSlice() {
				if v.Type() == cty.String {
					checks = append(checks, v.AsString())
				}
			}
		}
	}
	sort.Strings(checks)
	return checks, declared, nil
}

// jobNames is what each job of a workflow reports itself as: its name, or
// its key where it has none.
func jobNames(body []byte) ([]string, error) {
	var wf struct {
		Jobs map[string]struct {
			Name string `yaml:"name"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(body, &wf); err != nil {
		return nil, err
	}
	var out []string
	for key, job := range wf.Jobs {
		if job.Name != "" {
			out = append(out, job.Name)
			continue
		}
		out = append(out, key)
	}
	sort.Strings(out)
	return out, nil
}

func TestEveryCheckAMergeIsHeldToIsAJobThatRuns(t *testing.T) {
	checks, declared, err := requiredChecksIn(tofuSources(t))
	if err != nil {
		t.Fatal(err)
	}
	if declared == 0 || len(checks) == 0 {
		t.Fatalf("nothing declares %s, so nothing here says which checks a merge is held to", requiredChecksDeclared)
	}
	reported := map[string]bool{}
	workflows := 0
	for file, body := range workflowTexts(t) {
		names, err := jobNames([]byte(body))
		if err != nil {
			t.Fatalf("%s does not parse: %v", file, err)
		}
		workflows++
		for _, n := range names {
			reported[n] = true
		}
	}
	if workflows == 0 {
		t.Fatal("no workflow was read, so this checked nothing")
	}
	for _, c := range checks {
		if !reported[c] {
			t.Errorf("a merge is held to a check named %q, and no job in any workflow reports under that name. Every pull request would wait on it for ever. Name the job as it is, or take the check out.", c)
		}
	}
}

// The reading, on a list and workflows made up here.
func TestARequiredCheckIsReadAndAJobIsKnownByItsNameOrItsKey(t *testing.T) {
	checks, declared, err := requiredChecksIn(map[string]string{"made-up.tf": `
locals {
  something_else  = ["ignored"]
  required_checks = ["A named job", "a-keyed-job"]
}
locals {
  required_checks = var.worked_out_elsewhere
}
`})
	if err != nil || declared != 1 || len(checks) != 2 || checks[0] != "A named job" {
		t.Errorf("read %v from %d declaration(s), %v", checks, declared, err)
	}
	names, err := jobNames([]byte("jobs:\n  a-keyed-job:\n    runs-on: x\n  other:\n    name: A named job\n"))
	if err != nil || len(names) != 2 || names[0] != "A named job" || names[1] != "a-keyed-job" {
		t.Errorf("the jobs were read as %v, %v", names, err)
	}
	if _, err := jobNames([]byte("jobs: [")); err == nil {
		t.Error("a workflow that does not parse was read as having no jobs")
	}
	if _, _, err := requiredChecksIn(map[string]string{"broken.tf": `locals {`}); err == nil {
		t.Error("OpenTofu that does not parse was read as declaring nothing")
	}
}
