package repo

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"gopkg.in/yaml.v3"

	"homelab/details/ghapi"
)

// Every workflow that asks for one of the estate's own runners is one their
// group admits, and the group admits no other.
//
// WHY. The estate sets, at its source control, which workflows may use its
// runners, and the platform gives a runner to no other. A workflow moved
// onto the estate's runners without being named there is found out by the
// platform after the merge, as a job that waits for a runner for ever; and
// a name left there after its workflow stopped using them is a workflow
// that may reach the estate's network with nobody having decided it should.
//
// IT ALSO HOLDS THE GROUP TO BEING A WALL: one that admits chosen
// repositories and chosen workflows, written as such.
//
// ONE GROUP MAY TAKE ANY WORKFLOW, and only one kind: the group the pools
// of pull-request runners join. A pull request runs its own copy of a
// workflow, from its own ref, so no list of workflows at a branch can
// admit it. That group is held another way - its runners are on a machine
// closed to the rest of the estate - and it is allowed here only while
// every set of runners that joins it is a pull-request pool, and none of
// the estate's own does.
//
// FOUND BY WHAT IT IS. The group is whatever resource of that type is
// declared anywhere, and the names are whatever workflow files are written
// out where a group's workflows are given. Neither found is a failure.

// whose is which kind of runner a job asks for: one the platform hosts,
// one of the pools for a pull request's jobs, or one of the estate's own.
type whose int

const (
	thePlatforms whose = iota
	aPullRequestPools
	theEstatesOwn
)

// asksFor reads a job's runs-on. Anything that cannot be read as a label -
// a group, or nothing - is the estate's own: nothing here can say it is not.
func asksFor(n *yaml.Node) whose {
	of := func(label string) whose {
		switch {
		case hostedLabel.MatchString(label):
			return thePlatforms
		case pullRequestPools[label]:
			return aPullRequestPools
		}
		return theEstatesOwn
	}
	switch n.Kind {
	case yaml.ScalarNode, yaml.SequenceNode:
		runners := runnersOf(n)
		if len(runners) == 0 {
			return theEstatesOwn
		}
		most := thePlatforms
		for _, label := range runners {
			most = max(most, of(label))
		}
		return most
	case 0:
		return thePlatforms
	}
	return theEstatesOwn
}

// onTheEstatesRunners is whether any job of a workflow asks for one of the
// estate's own runners, and whether any asks for a pull-request pool.
func onTheEstatesRunners(docs []any) (own, pools bool, err error) {
	var wf workflowFile
	if len(docs) != 1 {
		return false, false, fmt.Errorf("a workflow is one document, and this is %d", len(docs))
	}
	if err := as(docs[0], &wf); err != nil {
		return false, false, err
	}
	for _, job := range wf.Jobs {
		switch asksFor(&job.RunsOn) {
		case theEstatesOwn:
			own = true
		case aPullRequestPools:
			pools = true
		}
	}
	return own, pools, nil
}

// admittedBy reads the OpenTofu given for every group of runners and every
// workflow file named where a group's workflows are given. It returns the
// names, sorted; what stops any group being a wall; and the name of every
// group that takes any workflow, which the caller holds to being the
// pull-request pools'.
func admittedBy(sources map[string]*hcl.File) (names []string, wrong []string, open []string, groups int, err error) {
	seen := map[string]bool{}
	for path, file := range sources {
		body, ok := file.Body.(*hclsyntax.Body)
		if !ok {
			return nil, nil, nil, 0, fmt.Errorf("%s is not native OpenTofu syntax", path)
		}
		for _, block := range body.Blocks {
			for _, given := range []string{"workflows", "selected_workflows"} {
				attr, has := block.Body.Attributes[given]
				if !has {
					continue
				}
				_ = hclsyntax.VisitAll(attr.Expr, func(node hclsyntax.Node) hcl.Diagnostics {
					if lit, is := node.(*hclsyntax.LiteralValueExpr); is && lit.Val.Type() == cty.String && !lit.Val.IsNull() {
						if v := lit.Val.AsString(); strings.HasSuffix(v, ".yml") || strings.HasSuffix(v, ".yaml") {
							seen[v] = true
						}
					}
					return nil
				})
			}
			if block.Type != "resource" || len(block.Labels) != 2 || block.Labels[0] != ghapi.RunnerGroupType {
				continue
			}
			groups++
			where := path + ": " + block.Labels[0] + "." + block.Labels[1]
			if written(block.Body, "visibility") != cty.StringVal("selected") {
				wrong = append(wrong, where+" admits repositories nobody chose: visibility is not \"selected\"")
			}
			// A group written as taking any workflow says which it is, by a
			// name written out, so that who joins it can be looked at.
			if written(block.Body, "restricted_to_workflows") == cty.False {
				if name := written(block.Body, "name"); name.Type() == cty.String && !name.IsNull() {
					open = append(open, name.AsString())
				} else {
					wrong = append(wrong, where+" admits any workflow, and its name is worked out, so nothing can say it is the group the pull-request pools join")
				}
				continue
			}
			if written(block.Body, "restricted_to_workflows") != cty.True {
				wrong = append(wrong, where+" admits any workflow: restricted_to_workflows is not true")
			}
			if _, has := block.Body.Attributes["selected_workflows"]; !has {
				wrong = append(wrong, where+" names no workflow it admits: selected_workflows is not set")
			}
		}
	}
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	sort.Strings(wrong)
	sort.Strings(open)
	return names, wrong, open, groups, nil
}

// heldAsPullRequestGroups is what is wrong with the groups that take any
// workflow, given which sets of runners join each group and whether each
// set is a pull-request pool.
func heldAsPullRequestGroups(open []string, joined map[string][]bool, usesAPool bool) []string {
	var out []string
	isOpen := map[string]bool{}
	for _, name := range open {
		isOpen[name] = true
		sets := joined[name]
		if len(sets) == 0 {
			out = append(out, "the group "+name+" takes any workflow and no set of runners joins it: a group for nothing")
		}
		for _, isPool := range sets {
			if !isPool {
				out = append(out, "the group "+name+" takes any workflow, and a set of the estate's own runners joins it. Only the pull-request pools, on a machine of their own, may be in a group no list of workflows holds")
			}
		}
	}
	for group, sets := range joined {
		for _, isPool := range sets {
			if isPool && !isOpen[group] {
				out = append(out, "a pull-request pool joins the group "+group+", which takes only named workflows from a branch. A pull request runs from neither, so its jobs would wait for a runner for ever")
			}
		}
	}
	if usesAPool && len(open) == 0 {
		out = append(out, "a workflow asks for a pull-request pool and no group is declared for the pools to join")
	}
	sort.Strings(out)
	return out
}

// notTheSame is what one sorted list of workflows holds that the other
// does not, each said the way it is wrong.
func notTheSame(asking, admitted []string) []string {
	in := func(list []string, s string) bool {
		i := sort.SearchStrings(list, s)
		return i < len(list) && list[i] == s
	}
	var out []string
	for _, w := range asking {
		if !in(admitted, w) {
			out = append(out, w+" asks for the estate's runner and no group admits it: it would wait for a runner for ever. Name it where the group's workflows are given")
		}
	}
	for _, w := range admitted {
		if !in(asking, w) {
			out = append(out, w+" is admitted to the estate's runners and no job of it asks for one. Take it out of the group's workflows")
		}
	}
	return out
}

func TestEveryWorkflowOnTheEstatesRunnersIsOneTheirGroupAdmits(t *testing.T) {
	light(t)
	tree := repository(t)

	sources := map[string]*hcl.File{}
	var asking []string
	workflows, usesAPool := 0, false
	for _, rel := range tree.Files(t) {
		if !authoredHere(t, rel) {
			continue
		}
		isTofu := strings.HasSuffix(rel, ".tf")
		isWorkflow := runnable(rel) && filepath.Base(rel) != "action.yml" && filepath.Base(rel) != "action.yaml"
		if !isTofu && !isWorkflow {
			continue
		}
		if isTofu {
			sources[rel] = tree.HCL(t, rel)
			continue
		}
		workflows++
		own, pools, err := onTheEstatesRunners(tree.YAMLDocuments(t, rel))
		if err != nil {
			t.Fatalf("%s does not parse: %v", rel, err)
		}
		if own {
			asking = append(asking, filepath.Base(rel))
		}
		usesAPool = usesAPool || pools
	}
	sort.Strings(asking)

	const fewestWorkflows = 5
	if workflows < fewestWorkflows {
		t.Fatalf("only %d workflow(s) were read, so this checked almost nothing", workflows)
	}
	if len(asking) == 0 {
		t.Fatal("no workflow asks for the estate's runner, and a converge runs on one: the read is wrong")
	}
	admitted, wrong, open, groups, err := admittedBy(sources)
	if err != nil {
		t.Fatal(err)
	}
	if groups == 0 {
		t.Fatalf("no %s is declared anywhere, so nothing here says which work the estate's runners take", ghapi.RunnerGroupType)
	}

	// Which group each set of runners joins, and whether the set is one of
	// the pull-request pools: by the name a job asks for it by.
	joined := map[string][]bool{}
	for _, release := range runnerSets(t) {
		_, manifest := fluxObject(t, kindHelmRelease, release)
		set := yamlScalar(t, manifest, "runnerScaleSetName")
		isPool := false
		for pool := range pullRequestPools {
			prefix, _, _ := strings.Cut(pool, "${{")
			isPool = isPool || strings.HasPrefix(set, prefix)
		}
		group := yamlScalar(t, manifest, "runnerGroup")
		joined[group] = append(joined[group], isPool)
	}
	wrong = append(wrong, heldAsPullRequestGroups(open, joined, usesAPool)...)
	for _, w := range append(wrong, notTheSame(asking, admitted)...) {
		t.Error(w)
	}
}

// The reading and the judging, on groups and workflows made up here.
func TestAGroupOfRunnersIsAWallOnlyWhileItNamesWhatItAdmits(t *testing.T) {
	light(t)
	const group = `
resource "github_actions_runner_group" "one" {
  name                    = var.name
  visibility              = "selected"
  restricted_to_workflows = true
  selected_workflows      = var.workflows
}
module "runners" {
  source    = "./somewhere"
  workflows = [for w in ["builds.yml", "tests.yaml"] : "${var.owner}/thing/.github/workflows/${w}@refs/heads/trunk"]
}
resource "something_else" "ignored" { visibility = "all" }
`
	for name, c := range map[string]struct {
		change func(string) string
		says   string
	}{
		"a wall":                  {func(s string) string { return s }, ""},
		"every repository":        {func(s string) string { return strings.Replace(s, `"selected"`, `"all"`, 1) }, "repositories nobody chose"},
		"any workflow":            {func(s string) string { return strings.Replace(s, "= true", "= false", 1) }, "its name is worked out"},
		"restriction left unsaid": {func(s string) string { return strings.Replace(s, "  restricted_to_workflows = true\n", "", 1) }, "admits any workflow"},
		"no workflows given":      {func(s string) string { return strings.Replace(s, "  selected_workflows      = var.workflows\n", "", 1) }, "names no workflow"},
	} {
		names, wrong, _, groups, err := admittedBy(fixtureHCL(t, map[string]string{"made-up.tf": c.change(group)}))
		if err != nil || groups != 1 {
			t.Fatalf("%s: read %d group(s), %v", name, groups, err)
		}
		if got := strings.Join(names, " "); got != "builds.yml tests.yaml" {
			t.Errorf("%s: the workflows named were read as %q", name, got)
		}
		said := strings.Join(wrong, "\n")
		if (c.says == "") != (said == "") || !strings.Contains(said, c.says) {
			t.Errorf("%s: want %q, got\n%s", name, c.says, said)
		}
	}

	// A group written as taking any workflow is not judged as a wall here;
	// it is named, for the caller to hold to being the pull-request pools'.
	_, wrong, open, _, err := admittedBy(fixtureHCL(t, map[string]string{"made-up.tf": `
resource "github_actions_runner_group" "any" {
  name                    = "for-pull-requests"
  visibility              = "selected"
  restricted_to_workflows = false
}
resource "github_actions_runner_group" "unnamed" {
  name                    = var.name
  visibility              = "selected"
  restricted_to_workflows = false
}
`}))
	if err != nil || len(open) != 1 || open[0] != "for-pull-requests" || len(wrong) != 1 || !strings.Contains(wrong[0], "its name is worked out") {
		t.Errorf("the groups that take any workflow were read as %v, with %v, %v", open, wrong, err)
	}
	for name, c := range map[string]struct {
		open      []string
		joined    map[string][]bool
		usesAPool bool
		says      string
	}{
		"the pools' own group":           {[]string{"g"}, map[string][]bool{"g": {true, true}, "walled": {false}}, true, ""},
		"a group nothing joins":          {[]string{"g"}, map[string][]bool{}, false, "no set of runners joins it"},
		"the estate's own runners in it": {[]string{"g"}, map[string][]bool{"g": {true, false}}, true, "a set of the estate's own runners joins it"},
		"a pool in the walled group":     {nil, map[string][]bool{"walled": {false, true}}, true, "would wait for a runner for ever"},
		"pools asked for and no group":   {nil, map[string][]bool{"walled": {false}}, true, "no group is declared for the pools to join"},
	} {
		said := strings.Join(heldAsPullRequestGroups(c.open, c.joined, c.usesAPool), "\n")
		if (c.says == "") != (said == "") || !strings.Contains(said, c.says) {
			t.Errorf("%s: want %q, got\n%s", name, c.says, said)
		}
	}

	pool := pullRequestPool("source")
	for name, c := range map[string]struct {
		yaml       string
		own, pools bool
	}{
		"hosted only":         {"jobs:\n  a:\n    runs-on: ubuntu-24.04\n  b:\n    runs-on: [ubuntu-latest]\n", false, false},
		"a named set":         {"jobs:\n  a:\n    runs-on: ubuntu-24.04\n  b:\n    runs-on: some-set\n", true, false},
		"a set worked out":    {"jobs:\n  a:\n    runs-on: some-${{ matrix.site }}\n", true, false},
		"a group":             {"jobs:\n  a:\n    runs-on:\n      group: some-group\n", true, false},
		"a list with one own": {"jobs:\n  a:\n    runs-on: [ubuntu-latest, some-set]\n", true, false},
		"a pull-request pool": {"jobs:\n  a:\n    runs-on: " + pool + "\n", false, true},
		"a pool and its own":  {"jobs:\n  a:\n    runs-on: " + pool + "\n  b:\n    runs-on: some-set\n", true, true},
	} {
		own, pools, err := onTheEstatesRunners(fixtureDocuments(t, c.yaml))
		if err != nil || own != c.own || pools != c.pools {
			t.Errorf("%s: got %v, %v, %v", name, own, pools, err)
		}
	}

	got := strings.Join(notTheSame([]string{"builds.yml", "moved.yml"}, []string{"builds.yml", "stale.yml"}), "\n")
	if !strings.Contains(got, "moved.yml asks for the estate's runner and no group admits it") || !strings.Contains(got, "stale.yml is admitted") || strings.Contains(got, "builds.yml") {
		t.Errorf("the difference was said as\n%s", got)
	}
}

// fixtureHCL is OpenTofu a test writes out, parsed as the tree parses its own.
func fixtureHCL(t *testing.T, files map[string]string) map[string]*hcl.File {
	t.Helper()
	out := map[string]*hcl.File{}
	for path, body := range files {
		file, diags := hclsyntax.ParseConfig([]byte(body), path, hcl.InitialPos)
		if diags.HasErrors() {
			t.Fatalf("a fixture is not OpenTofu: %s", diags.Error())
		}
		out[path] = file
	}
	return out
}
