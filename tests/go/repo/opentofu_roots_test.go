package repo

import (
	"fmt"
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"homelab/contractor/config"
	"homelab/tests/parsed"
)

// A new OpenTofu root is seen by everything that has to see one.
//
// WHY THIS EXISTS. The site's root was split in two, and the checks that knew
// about "the root" went on knowing about one. The pull request checks
// validated one directory; the one guard that listed every secret OpenTofu
// creates read one directory, found none after the move, and went on passing;
// and a validation in the new root counted as tested because a test in the old
// root named a variable of the same name. Nothing was removed. Each of those
// was a list of one, written when there was one.
//
// A guard that reads named files guards what was known when it was written.
// This one is for what was not: it finds the roots by what a root is - a
// directory with a provider block - and holds each to the list below. A root
// nobody has thought about fails every line of it at once, which is the point.
func TestEveryOpenTofuRootIsSeenByWhatMustSeeARoot(t *testing.T) {
	t.Parallel()
	tree := repository(t)
	files := tree.Files(t)
	roots := parsed.OpenTofuRoots(tree.OpenTofu(t))
	if len(roots) < 3 {
		t.Fatalf("found %d OpenTofu root(s) (%v), and this estate has at least three, so the enumeration has stopped matching", len(roots), roots)
	}
	for _, r := range roots {
		for _, p := range sightingsOf(t, tree, files).problems(r) {
			t.Error(p)
		}
	}

	// A site's roots are the ones the contractor runs, and every one it runs
	// is a root: its sterilize, backup, restore and record all walk
	// config.Roots, so a root missing from it keeps its state on disk after a
	// run and is in no backup.
	for _, p := range siteRootProblems(roots, config.Roots) {
		t.Error(p)
	}
}

// estateRootDir is the one root that is not a site's: the lawyer runs it, with
// the estate's credentials, and the contractor never does.
const estateRootDir = "management/estate"

// rootSightings is everything that has to know a root exists, each as its
// own parser reads it.
type rootSightings struct {
	tracked []string
	tofu    []parsed.OpenTofuFile
	// Each directory a step of the pull request checks runs in, each a
	// task validates OpenTofu in, each Dependabot watches, and each path
	// .gitignore leaves out.
	checkedIn, validatedIn, watched, ignored map[string]bool
}

// sightingsOf reads what has to see a root from the repository.
func sightingsOf(t *testing.T, tree *parsed.Tree, files []string) rootSightings {
	t.Helper()
	s := rootSightings{tracked: files, tofu: tree.OpenTofu(t),
		checkedIn: map[string]bool{}, validatedIn: map[string]bool{}, watched: map[string]bool{}, ignored: map[string]bool{}}

	var workflow struct {
		Jobs map[string]struct {
			Defaults struct {
				Run struct {
					Dir string `yaml:"working-directory"`
				} `yaml:"run"`
			} `yaml:"defaults"`
			Steps []struct {
				Dir string `yaml:"working-directory"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	tree.YAMLInto(t, ".github/workflows/pr-validation.yml", &workflow)
	for _, job := range workflow.Jobs {
		s.checkedIn[job.Defaults.Run.Dir] = true
		for _, step := range job.Steps {
			s.checkedIn[step.Dir] = true
		}
	}

	var taskfile struct {
		Tasks map[string]struct {
			Cmds []any `yaml:"cmds"`
		} `yaml:"tasks"`
	}
	tree.YAMLInto(t, "taskfile.yml", &taskfile)
	for _, task := range taskfile.Tasks {
		for _, c := range task.Cmds {
			cmd, _ := c.(string)
			if m, ok := c.(map[string]any); ok {
				cmd, _ = m["cmd"].(string)
			}
			commands, err := parsed.WorkflowCommands("taskfile.yml", cmd)
			if err != nil {
				t.Fatalf("taskfile.yml writes down a command that is not shell that can be read: %v", err)
			}
			for _, c := range commands {
				if dir, ok := validates(c); ok {
					s.validatedIn[dir] = true
				}
			}
		}
	}

	var dependabot struct {
		Updates []struct {
			Directory string `yaml:"directory"`
		} `yaml:"updates"`
	}
	tree.YAMLInto(t, ".github/dependabot.yml", &dependabot)
	for _, u := range dependabot.Updates {
		s.watched[strings.TrimPrefix(u.Directory, "/")] = true
	}

	entries, _ := tree.Conftest(t, ".gitignore", "ignore").([]any)
	for _, e := range entries {
		if entry, _ := e.(map[string]any); entry["Kind"] == "Path" {
			p, _ := entry["Value"].(string)
			s.ignored[strings.TrimPrefix(p, "/")] = true
		}
	}
	return s
}

// validates is the directory a command validates OpenTofu in, when it does:
// tofu -chdir=<dir> validate.
func validates(c parsed.Command) (string, bool) {
	if len(c.Words) < 3 || c.Words[0] != "tofu" || !slices.Contains(c.Words[1:], "validate") {
		return "", false
	}
	for _, w := range c.Words[1:] {
		if dir, ok := strings.CutPrefix(w, "-chdir="); ok {
			return dir, true
		}
	}
	return "", false
}

// refusing is the kinds of block that make a root refuse something.
var refusing = map[string]bool{"validation": true, "precondition": true, "postcondition": true, "check": true}

// problems is each thing that should see the root and does not.
func (s rootSightings) problems(root string) []string {
	var out []string
	say := func(format string, args ...any) { out = append(out, root+" "+fmt.Sprintf(format, args...)) }

	if !slices.Contains(s.tracked, root+"/.terraform.lock.hcl") {
		say("has no committed .terraform.lock.hcl, so every machine resolves its providers for itself and none of them is pinned by hash.")
	}
	if !s.checkedIn[root] {
		say("is not validated by the pull request checks (no step in pr-validation.yml runs in it), so a change that does not parse there merges green.")
	}
	if !s.validatedIn[root] {
		say("is not validated by `task validate`, so the pre-push hook passes a change that does not parse there.")
	}
	if !s.watched[root] {
		say("has no entry in .github/dependabot.yml, so its providers are never offered an update and a vulnerable one stays pinned.")
	}

	// A backend a run switches on by copying a file into place is a file a
	// failed run leaves behind, and it must never be committed.
	for _, rel := range s.tracked {
		if on, switched := strings.CutSuffix(rel, ".disabled"); switched && path.Dir(rel) == root && strings.HasSuffix(on, ".tf") && !s.ignored[on] {
			say("switches its backend on by copying %s into place, and .gitignore does not list %s, so a run that stops partway leaves a file that commits the estate's backend into the repository.", path.Base(rel), on)
		}
	}
	asserts := false
	for _, f := range s.tofu {
		if path.Dir(f.Path) != root {
			continue
		}
		_ = hclsyntax.VisitAll(f.Body, func(n hclsyntax.Node) hcl.Diagnostics {
			if b, ok := n.(*hclsyntax.Block); ok && refusing[b.Type] {
				asserts = true
			}
			return nil
		})
	}

	// A root that refuses something has its own test of the refusal. The
	// per-file check is TestEveryUnitIsExecutedByATestOrIsADeclaredDebt; this
	// is the plainer question of whether the root is run by a test at all.
	if asserts {
		tested := false
		for _, rel := range s.tracked {
			if strings.HasSuffix(rel, ".tftest.hcl") && testsTheRootOf(rel, root+"/x.tf") {
				tested = true
			}
		}
		if !tested {
			say("declares a validation or a precondition and has no .tftest.hcl of its own, so nothing has ever seen it refuse.")
		}
	}
	return out
}

// siteRootProblems holds the roots the repository has to the roots the
// contractor runs: every root but the estate's is one of a site's, and every
// one of a site's exists.
func siteRootProblems(roots, siteRoots []string) []string {
	var out []string
	for _, r := range roots {
		if r == estateRootDir {
			continue
		}
		if name := strings.TrimPrefix(r, "management/"); name == r || !slices.Contains(siteRoots, name) {
			out = append(out, r+" is an OpenTofu root the contractor does not know: it is not management/<one of config.Roots>. Its state is not sterilized after a run, not backed up, not restored and not in the as-built record, and no step applies it.")
		}
	}
	for _, name := range siteRoots {
		if !slices.Contains(roots, "management/"+name) {
			out = append(out, "config.Roots names "+name+", and management/"+name+" is not an OpenTofu root, so the contractor would run tofu in a directory that holds none.")
		}
	}
	return out
}

// tofuOf is OpenTofu a test writes, parsed as the repository's is.
func tofuOf(t *testing.T, files map[string]string) []parsed.OpenTofuFile {
	t.Helper()
	tree := parsed.Empty(t)
	for rel, body := range files {
		tree.Write(t, rel, body)
	}
	tree.Index(t)
	return tree.OpenTofu(t)
}

func TestRootSightingsNamesEachThingThatCannotSeeARoot(t *testing.T) {
	t.Parallel()
	const r = "management/new"
	set := func(v string) map[string]bool { return map[string]bool{v: true} }
	whole := rootSightings{
		tracked: []string{r + "/.terraform.lock.hcl", r + "/tests/new.tftest.hcl", r + "/backend_pg.tf.disabled"},
		tofu: tofuOf(t, map[string]string{
			r + "/access.tf": "provider \"x\" {}\n",
			r + "/inputs.tf": "variable \"a\" {\n  validation {\n  }\n}\n",
		}),
		checkedIn:   set(r),
		validatedIn: set(r),
		watched:     set(r),
		ignored:     set(r + "/backend_pg.tf"),
	}
	if got := whole.problems(r); len(got) != 0 {
		t.Fatalf("a root everything sees was refused: %v", got)
	}
	if got := parsed.OpenTofuRoots(whole.tofu); !slices.Equal(got, []string{r}) {
		t.Errorf("roots are %v", got)
	}
	for name, tc := range map[string]struct {
		blind func(*rootSightings)
		want  string
	}{
		"no lock file":          {func(s *rootSightings) { s.tracked = s.tracked[1:] }, "no committed .terraform.lock.hcl"},
		"not validated in CI":   {func(s *rootSightings) { s.checkedIn = set("management/other") }, "not validated by the pull request checks"},
		"not validated at push": {func(s *rootSightings) { s.validatedIn = nil }, "not validated by `task validate`"},
		"no provider updates":   {func(s *rootSightings) { s.watched = set(r + "-other") }, "no entry in .github/dependabot.yml"},
		"backend not ignored":   {func(s *rootSightings) { s.ignored = nil }, "does not list " + r + "/backend_pg.tf"},
		"asserts and untested": {func(s *rootSightings) {
			s.tracked = []string{r + "/.terraform.lock.hcl", r + "/backend_pg.tf.disabled"}
		}, "no .tftest.hcl of its own"},
	} {
		blind := whole
		tc.blind(&blind)
		got := blind.problems(r)
		if len(got) != 1 || !strings.Contains(got[0], tc.want) {
			t.Errorf("%s: got %v, want one problem saying %q", name, got, tc.want)
		}
	}
	// What a task validates is read from its words, not from a mention.
	for cmd, want := range map[string]string{"tofu -chdir=" + r + " validate": r, "echo tofu -chdir=" + r + " validate": "", "tofu -chdir=" + r + " plan": ""} {
		c, _ := parsed.WorkflowCommands("taskfile.yml", cmd)
		if got, _ := validates(c[0]); got != want {
			t.Errorf("%q validates %q, want %q", cmd, got, want)
		}
	}
}

func TestSiteRootProblemsHoldsTheRootsToTheContractors(t *testing.T) {
	t.Parallel()
	site := config.Roots
	cluster, platform := "management/"+config.ClusterRoot, "management/"+config.PlatformRoot
	for name, tc := range map[string]struct {
		roots []string
		want  string
	}{
		"as built":                    {[]string{cluster, estateRootDir, platform}, ""},
		"a root the contractor lacks": {[]string{cluster, platform, "management/extra"}, "management/extra is an OpenTofu root the contractor does not know"},
		"a root somewhere else":       {[]string{cluster, platform, "elsewhere/root"}, "elsewhere/root is an OpenTofu root the contractor does not know"},
		"a named root that is gone":   {[]string{cluster}, "config.Roots names " + config.PlatformRoot},
	} {
		got := strings.Join(siteRootProblems(tc.roots, site), "|")
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}
