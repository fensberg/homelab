package repo

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// Every action a workflow here uses is one the repository is set to allow.
//
// WHY. The estate sets, at its source control, which actions a workflow in a
// repository may run, and the platform refuses any other before a line of it
// runs. That is a wall around the repository and it is set from one list. A
// workflow that names an action the list does not hold is found out by the
// platform, after the merge, as a job that never starts; this finds it
// before.
//
// IT ALSO HOLDS THE WALL TO BEING ONE. A list is only a wall while the
// setting says "selected" and "at a full commit" and does not also wave in
// every publisher the platform has verified. Each of those is one word in a
// file, so each is held here.
//
// FOUND BY WHAT IT IS. The setting is whatever resource of that type is
// declared anywhere in the repository, not a file this names. None found is
// a failure: a guard that could not find the wall has not checked it.
const allowedActionsType = "github_actions_repository_permissions"

// allowance is what one declared setting lets a workflow run.
type allowance struct {
	where       string
	selected    bool
	pinned      bool
	verified    bool
	githubOwned bool
	patterns    []string
}

// allowancesIn is every such setting declared in the OpenTofu given.
func allowancesIn(sources map[string]string) ([]allowance, error) {
	var out []allowance
	for path, body := range sources {
		file, diags := hclsyntax.ParseConfig([]byte(body), path, hcl.InitialPos)
		if diags.HasErrors() {
			return nil, fmt.Errorf("%s does not parse: %s", path, diags.Error())
		}
		for _, block := range file.Body.(*hclsyntax.Body).Blocks {
			if block.Type != "resource" || len(block.Labels) != 2 || block.Labels[0] != allowedActionsType {
				continue
			}
			a := allowance{where: path + ": " + block.Labels[0] + "." + block.Labels[1]}
			a.selected = written(block.Body, "allowed_actions") == cty.StringVal("selected")
			a.pinned = written(block.Body, "sha_pinning_required") == cty.True
			for _, inner := range block.Body.Blocks {
				if inner.Type != "allowed_actions_config" {
					continue
				}
				a.githubOwned = written(inner.Body, "github_owned_allowed") == cty.True
				// Anything but a written "false" lets them in, as the
				// platform would read it.
				a.verified = written(inner.Body, "verified_allowed") != cty.False
				if list := written(inner.Body, "patterns_allowed"); list.IsKnown() && !list.IsNull() && list.CanIterateElements() {
					for _, v := range list.AsValueSlice() {
						if v.Type() == cty.String {
							a.patterns = append(a.patterns, v.AsString())
						}
					}
				}
			}
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].where < out[j].where })
	return out, nil
}

// written is an attribute's value where it is written out, and null where
// it is absent or worked out from something else.
func written(body *hclsyntax.Body, name string) cty.Value {
	attr, has := body.Attributes[name]
	if !has {
		return cty.NullVal(cty.DynamicPseudoType)
	}
	v, diags := attr.Expr.Value(nil)
	if diags.HasErrors() {
		return cty.NullVal(cty.DynamicPseudoType)
	}
	return v
}

// thePlatformsOwn are the organisations whose actions the platform counts
// as its own.
var thePlatformsOwn = map[string]bool{"actions": true, "github": true}

// allows reports whether an action, as "owner/repository", may run.
func (a allowance) allows(action string) bool {
	owner, _, _ := strings.Cut(action, "/")
	if a.githubOwned && thePlatformsOwn[owner] {
		return true
	}
	for _, p := range a.patterns {
		if p == action+"@*" {
			return true
		}
	}
	return false
}

// wrongWith is everything that stops a setting being a wall, and every
// action of those used that it would refuse.
func (a allowance) wrongWith(used []string) []string {
	var out []string
	if !a.selected {
		out = append(out, "it does not hold workflows to a list: allowed_actions is not \"selected\"")
	}
	if !a.pinned {
		out = append(out, "it lets an action run from a tag or a branch: sha_pinning_required is not true")
	}
	if a.verified {
		out = append(out, "it lets in every publisher the platform has verified: verified_allowed is not false")
	}
	for _, p := range a.patterns {
		if repo, at, _ := strings.Cut(p, "@"); at != "*" || strings.Count(repo, "/") != 1 || strings.Contains(repo, "*") {
			out = append(out, "it allows "+p+", which is wider than one action: write owner/repository@*")
		}
	}
	for _, u := range used {
		if !a.allows(u) {
			out = append(out, "a workflow uses "+u+", which it would refuse: add \""+u+"@*\" to patterns_allowed")
		}
	}
	return out
}

// actionsUsed is every action, as "owner/repository", that a workflow or a
// composite action's text names from outside the repository.
func actionsUsed(bodies []string) []string {
	seen := map[string]bool{}
	for _, body := range bodies {
		for _, m := range usesLine.FindAllStringSubmatch(body, -1) {
			ref := m[1]
			if strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "$/") || strings.HasPrefix(ref, "docker://") {
				continue
			}
			name, _, _ := strings.Cut(ref, "@")
			parts := strings.SplitN(name, "/", 3)
			if len(parts) < 2 {
				continue
			}
			seen[parts[0]+"/"+parts[1]] = true
		}
	}
	var out []string
	for a := range seen {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

func TestEveryActionAWorkflowUsesIsOneTheRepositoryAllows(t *testing.T) {
	root := repoRoot(t)

	sources := map[string]string{}
	var runnables []string
	for _, rel := range trackedFilesIn(t, root) {
		if !authoredHere(rel) {
			continue
		}
		if !strings.HasSuffix(rel, ".tf") && !runnable(rel) {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		if strings.HasSuffix(rel, ".tf") {
			sources[rel] = string(body)
		} else {
			runnables = append(runnables, string(body))
		}
	}

	used := actionsUsed(runnables)
	const fewestActionsUsed = 5
	if len(used) < fewestActionsUsed {
		t.Fatalf("only %d action(s) were found in the workflows, so this checked almost nothing", len(used))
	}
	allowances, err := allowancesIn(sources)
	if err != nil {
		t.Fatal(err)
	}
	if len(allowances) == 0 {
		t.Fatalf("no %s is declared anywhere, so nothing here says what a workflow may run", allowedActionsType)
	}
	for _, a := range allowances {
		for _, wrong := range a.wrongWith(used) {
			t.Errorf("%s: %s", a.where, wrong)
		}
	}
}

// The reading and the judging, on settings and workflows made up here.
func TestASettingIsAWallOnlyWhileItHoldsWorkflowsToAListAtACommit(t *testing.T) {
	const wall = `
resource "github_actions_repository_permissions" "one" {
  repository           = var.name
  allowed_actions      = "selected"
  sha_pinning_required = true
  allowed_actions_config {
    github_owned_allowed = true
    verified_allowed     = false
    patterns_allowed     = ["someone/builds@*"]
  }
}
resource "something_else" "ignored" { allowed_actions = "all" }
`
	used := actionsUsed([]string{`
jobs:
  a:
    steps:
      - uses: actions/fetch@0123456789012345678901234567890123456789 # v1
      - uses: github/scan/start@0123456789012345678901234567890123456789
      - uses: "someone/builds@0123456789012345678901234567890123456789"
      - uses: ./.github/workflows/local
      - uses: $/.github/workflows/shared
      - uses: docker://example.test/image:1
`})
	if got := strings.Join(used, " "); got != "actions/fetch github/scan someone/builds" {
		t.Fatalf("the actions used were read as %q", got)
	}

	for name, c := range map[string]struct {
		change func(string) string
		used   []string
		says   string
	}{
		"a wall that holds":           {func(s string) string { return s }, used, ""},
		"any action":                  {func(s string) string { return strings.Replace(s, `"selected"`, `"all"`, 1) }, used, "does not hold workflows to a list"},
		"a list worked out elsewhere": {func(s string) string { return strings.Replace(s, `"selected"`, `var.which`, 1) }, used, "does not hold workflows to a list"},
		"from a tag":                  {func(s string) string { return strings.Replace(s, "= true\n  allowed", "= false\n  allowed", 1) }, used, "from a tag or a branch"},
		"pinning left unsaid":         {func(s string) string { return strings.Replace(s, "  sha_pinning_required = true\n", "", 1) }, used, "from a tag or a branch"},
		"verified publishers":         {func(s string) string { return strings.Replace(s, "= false", "= true", 1) }, used, "every publisher the platform has verified"},
		"verified left unsaid":        {func(s string) string { return strings.Replace(s, "    verified_allowed     = false\n", "", 1) }, used, "every publisher the platform has verified"},
		"a whole organisation":        {func(s string) string { return strings.Replace(s, "someone/builds@*", "someone/*", 1) }, nil, "wider than one action"},
		"an action it would refuse":   {func(s string) string { return s }, append([]string{"another/thing"}, used...), "uses another/thing, which it would refuse"},
		"the platform's own, unwanted": {func(s string) string {
			return strings.Replace(s, "github_owned_allowed = true", "github_owned_allowed = false", 1)
		}, used, "uses actions/fetch, which it would refuse"},
	} {
		found, err := allowancesIn(map[string]string{"made-up.tf": c.change(wall)})
		if err != nil || len(found) != 1 {
			t.Fatalf("%s: read %d setting(s), %v", name, len(found), err)
		}
		wrong := strings.Join(found[0].wrongWith(c.used), "\n")
		if c.says == "" && wrong != "" {
			t.Errorf("%s: refused for\n%s", name, wrong)
		}
		if c.says != "" && !strings.Contains(wrong, c.says) {
			t.Errorf("%s: was not refused for %q; it said\n%s", name, c.says, wrong)
		}
	}

	if _, err := allowancesIn(map[string]string{"broken.tf": `resource "x" {`}); err == nil {
		t.Error("OpenTofu that does not parse was read as declaring nothing")
	}
}
