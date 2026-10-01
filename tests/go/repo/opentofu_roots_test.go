package repo

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"homelab/contractor/config"
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
	root := repoRoot(t)
	files := tracked(t, func(string) bool { return true })
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		return string(b)
	}
	sources := map[string]string{}
	for _, rel := range files {
		if strings.HasSuffix(rel, ".tf") || strings.HasSuffix(rel, ".tf.disabled") {
			sources[rel] = read(rel)
		}
	}
	roots := openTofuRoots(sources)
	if len(roots) < 3 {
		t.Fatalf("found %d OpenTofu root(s) (%v), and this estate has at least three, so the enumeration has stopped matching", len(roots), roots)
	}

	seen := rootSightings{
		tracked:    files,
		sources:    sources,
		workflow:   read(".github/workflows/pr-validation.yml"),
		taskfile:   read("taskfile.yml"),
		dependabot: read(".github/dependabot.yml"),
		gitignore:  read(".gitignore"),
	}
	for _, r := range roots {
		for _, p := range seen.problems(r) {
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

var providerBlock = regexp.MustCompile(`(?m)^provider\s+"`)

// openTofuRoots is every directory holding a provider block, which is what
// makes a directory a root rather than a module.
func openTofuRoots(sources map[string]string) []string {
	dirs := map[string]bool{}
	for rel, body := range sources {
		if strings.HasSuffix(rel, ".tf") && !strings.Contains(rel, "/tests/") && providerBlock.MatchString(body) {
			dirs[filepath.ToSlash(filepath.Dir(rel))] = true
		}
	}
	out := make([]string, 0, len(dirs))
	for d := range dirs {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// rootSightings is everything that has to know a root exists.
type rootSightings struct {
	tracked                                   []string
	sources                                   map[string]string
	workflow, taskfile, dependabot, gitignore string
}

var assertsInRoot = regexp.MustCompile(`(?m)^\s*(validation\s*\{|precondition\s*\{|postcondition\s*\{|check\s+")`)

// problems is each thing that should see the root and does not.
func (s rootSightings) problems(root string) []string {
	var out []string
	say := func(format string, args ...any) { out = append(out, root+" "+fmt.Sprintf(format, args...)) }

	if !slices.Contains(s.tracked, root+"/.terraform.lock.hcl") {
		say("has no committed .terraform.lock.hcl, so every machine resolves its providers for itself and none of them is pinned by hash.")
	}
	if !strings.Contains(s.workflow, "working-directory: "+root+"\n") {
		say("is not validated by the pull request checks (no step in pr-validation.yml runs in it), so a change that does not parse there merges green.")
	}
	if !strings.Contains(s.taskfile, "tofu -chdir="+root+" validate") {
		say("is not validated by `task validate`, so the pre-push hook passes a change that does not parse there.")
	}
	if !strings.Contains(s.dependabot, "directory: /"+root+"\n") {
		say("has no entry in .github/dependabot.yml, so its providers are never offered an update and a vulnerable one stays pinned.")
	}

	// A backend a run switches on by copying a file into place is a file a
	// failed run leaves behind, and it must never be committed.
	asserts := false
	for rel, body := range s.sources {
		if filepath.ToSlash(filepath.Dir(rel)) != root {
			continue
		}
		if on, switched := strings.CutSuffix(rel, ".disabled"); switched && !slices.Contains(strings.Split(s.gitignore, "\n"), on) {
			say("switches its backend on by copying %s into place, and .gitignore does not list %s, so a run that stops partway leaves a file that commits the estate's backend into the repository.", filepath.Base(rel), on)
		}
		if strings.HasSuffix(rel, ".tf") && assertsInRoot.MatchString(body) {
			asserts = true
		}
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

func TestRootSightingsNamesEachThingThatCannotSeeARoot(t *testing.T) {
	const r = "management/new"
	whole := rootSightings{
		tracked: []string{r + "/.terraform.lock.hcl", r + "/tests/new.tftest.hcl"},
		sources: map[string]string{
			r + "/access.tf":              "provider \"x\" {}\n",
			r + "/inputs.tf":              "variable \"a\" {\n  validation {\n  }\n}\n",
			r + "/backend_pg.tf.disabled": "terraform {}\n",
		},
		workflow:   "        working-directory: " + r + "\n",
		taskfile:   "      - tofu -chdir=" + r + " validate\n",
		dependabot: "    directory: /" + r + "\n",
		gitignore:  r + "/backend_pg.tf\n",
	}
	if got := whole.problems(r); len(got) != 0 {
		t.Fatalf("a root everything sees was refused: %v", got)
	}
	if got := openTofuRoots(whole.sources); !slices.Equal(got, []string{r}) {
		t.Errorf("roots are %v", got)
	}
	for name, tc := range map[string]struct {
		blind func(*rootSightings)
		want  string
	}{
		"no lock file":          {func(s *rootSightings) { s.tracked = s.tracked[1:] }, "no committed .terraform.lock.hcl"},
		"not validated in CI":   {func(s *rootSightings) { s.workflow = "working-directory: management/other\n" }, "not validated by the pull request checks"},
		"not validated at push": {func(s *rootSightings) { s.taskfile = "" }, "not validated by `task validate`"},
		"no provider updates":   {func(s *rootSightings) { s.dependabot = "directory: /" + r + "-other\n" }, "no entry in .github/dependabot.yml"},
		"backend not ignored":   {func(s *rootSightings) { s.gitignore = "" }, "does not list " + r + "/backend_pg.tf"},
		"asserts and untested":  {func(s *rootSightings) { s.tracked = s.tracked[:1] }, "no .tftest.hcl of its own"},
	} {
		blind := whole
		tc.blind(&blind)
		got := blind.problems(r)
		if len(got) != 1 || !strings.Contains(got[0], tc.want) {
			t.Errorf("%s: got %v, want one problem saying %q", name, got, tc.want)
		}
	}
}

func TestSiteRootProblemsHoldsTheRootsToTheContractors(t *testing.T) {
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
