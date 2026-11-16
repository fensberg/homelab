package repo

import (
	"fmt"
	"path"
	"slices"
	"strings"
	"testing"

	"homelab/tests/parsed"
)

// A run on main starts on every change to what it is built from.
//
// The estate's converge and the site's converge run on a merge, and only on
// a merge that touches the paths their workflow names. Each builds a Go
// program from this checkout and runs it, and that program is built from
// its own directory and from every local package its go.mod replaces in.
// The estate's converge named scripts/lawyer and not scripts/details, which
// the lawyer is built from; the site's named neither scripts/contractor nor
// scripts/details. A change to any of those merged, started nothing, and
// first ran on the next unrelated merge - where a failure offers that merge's
// revert, the wrong one (#885).
//
// So for every workflow that runs on a push with a paths filter, every Go
// program a step builds or runs with `go build -C dir` or `go run -C dir`,
// and every directory that program is built from, must be under one of the
// filter's paths. A program named in a way this cannot read is refused
// rather than passed by.
//
// Scope: what a workflow's steps run in themselves. A program a composite
// action builds is not followed here.
func TestEveryRunOnMainStartsOnWhatItIsBuiltFrom(t *testing.T) {
	light(t)
	tree := repository(t)
	filtered := 0
	for _, rel := range tree.Files(t) {
		if path.Dir(rel) != ".github/workflows" || !(strings.HasSuffix(rel, ".yml") || strings.HasSuffix(rel, ".yaml")) {
			continue
		}
		docs := tree.YAMLDocuments(t, rel)
		if len(docs) != 1 {
			t.Fatalf("%s is not one YAML document", rel)
		}
		doc, _ := docs[0].(map[string]any)
		paths, ok := pushPaths(doc)
		if !ok {
			continue
		}
		filtered++
		for _, dir := range programsBuilt(t, rel, doc) {
			for _, d := range uncovered(paths, tree.BuiltFrom(t, dir)) {
				t.Errorf("%s runs the program in %s, which is built from %s, and no path its push filter names covers %s: a change there merges and starts nothing. Add %q to its paths.", rel, dir, d, d, d+"/**")
			}
		}
	}
	if filtered == 0 {
		t.Fatal("no workflow runs on a push with a paths filter, so this checked nothing")
	}
}

// pushPaths is the paths a workflow's push trigger is filtered on, and
// whether it has a push trigger with a paths filter at all. YAML 1.1 reads
// the bare key `on` as true, so both spellings are read.
func pushPaths(doc map[string]any) ([]string, bool) {
	on, found := doc["on"]
	if !found {
		on = doc["true"]
	}
	triggers, _ := on.(map[string]any)
	push, _ := triggers["push"].(map[string]any)
	list, ok := push["paths"].([]any)
	if !ok {
		return nil, false
	}
	var out []string
	for _, p := range list {
		s, _ := p.(string)
		out = append(out, s)
	}
	return out, true
}

// programsBuilt is every directory a step of a workflow builds or runs a Go
// program from, by `go build -C dir` or `go run -C dir`.
func programsBuilt(t *testing.T, rel string, doc map[string]any) []string {
	t.Helper()
	var out []string
	jobs, _ := doc["jobs"].(map[string]any)
	for name, j := range jobs {
		job, _ := j.(map[string]any)
		steps, _ := job["steps"].([]any)
		for _, s := range steps {
			step, _ := s.(map[string]any)
			run, _ := step["run"].(string)
			if run == "" {
				continue
			}
			commands, err := parsed.WorkflowCommands(rel+" job "+name, run)
			if err != nil {
				t.Fatalf("%s job %s runs shell that cannot be read: %v", rel, name, err)
			}
			for _, c := range commands {
				dir, builds, err := goBuildDir(c.Words)
				if err != nil {
					t.Errorf("%s job %s: %v", rel, name, err)
				}
				if builds && !slices.Contains(out, dir) {
					out = append(out, dir)
				}
			}
		}
	}
	return out
}

// goBuildDir is the directory a `go build` or `go run` builds from, when the
// command is one: what its -C names. One with no -C, or a -C worked out as it
// runs, is an error, as which program it builds cannot be read.
func goBuildDir(words []string) (string, bool, error) {
	if len(words) < 2 || words[0] != "go" || (words[1] != "build" && words[1] != "run") {
		return "", false, nil
	}
	for i, w := range words[2:] {
		if w == "-C" && i+3 < len(words) {
			dir := words[i+3]
			if strings.ContainsAny(dir, "$*") {
				return "", false, fmt.Errorf("go %s -C %s names a directory worked out as it runs, so which program it builds cannot be read", words[1], dir)
			}
			return path.Clean(dir), true, nil
		}
	}
	return "", false, fmt.Errorf("go %s has no -C, so which program it builds cannot be read", words[1])
}

// uncovered is each directory no path of a filter covers. A path covers a
// directory when it is that directory, or one above it, followed by /**.
// A path with a wildcard anywhere else is not read as covering anything.
func uncovered(paths, dirs []string) []string {
	var out []string
	for _, d := range dirs {
		covered := false
		for _, p := range paths {
			base, whole := strings.CutSuffix(p, "/**")
			if whole && !strings.Contains(base, "*") && (d == base || strings.HasPrefix(d, base+"/")) {
				covered = true
			}
		}
		if !covered {
			out = append(out, d)
		}
	}
	return out
}

// The judging, on filters and commands made up here.
func TestAFilterCoversWhatItNamesAndWhatIsBelowIt(t *testing.T) {
	light(t)
	for _, c := range []struct {
		paths, dirs, want []string
	}{
		{[]string{"scripts/prog/**"}, []string{"scripts/prog"}, nil},
		{[]string{"scripts/**"}, []string{"scripts/prog", "scripts/shared"}, nil},
		{[]string{"scripts/prog/**"}, []string{"scripts/prog", "scripts/shared"}, []string{"scripts/shared"}},
		{[]string{"scripts/prog"}, []string{"scripts/prog"}, []string{"scripts/prog"}},
		{[]string{"scripts/*/**"}, []string{"scripts/prog"}, []string{"scripts/prog"}},
		{[]string{"scripts/pro/**"}, []string{"scripts/prog"}, []string{"scripts/prog"}},
	} {
		if got := uncovered(c.paths, c.dirs); !slices.Equal(got, c.want) {
			t.Errorf("%v over %v left %v uncovered, want %v", c.paths, c.dirs, got, c.want)
		}
	}
	for _, c := range []struct {
		words  []string
		dir    string
		builds bool
		fails  bool
	}{
		{[]string{"go", "run", "-C", "scripts/prog", ".", "verb"}, "scripts/prog", true, false},
		{[]string{"go", "build", "-C", "scripts/prog/", "-o", "out", "."}, "scripts/prog", true, false},
		{[]string{"go", "test", "./..."}, "", false, false},
		{[]string{"echo", "go", "run"}, "", false, false},
		{[]string{"go", "run", "."}, "", false, true},
		{[]string{"go", "run", "-C", "scripts/${{ matrix.prog }}", "."}, "", false, true},
	} {
		dir, builds, err := goBuildDir(c.words)
		if dir != c.dir || builds != c.builds || (err != nil) != c.fails {
			t.Errorf("%v read as %q, %v, %v", c.words, dir, builds, err)
		}
	}
	paths, ok := pushPaths(map[string]any{"true": map[string]any{"push": map[string]any{"paths": []any{"a/**"}}}})
	if !ok || !slices.Equal(paths, []string{"a/**"}) {
		t.Errorf("a push filter under the key YAML 1.1 reads `on` as was read as %v, %v", paths, ok)
	}
	if _, ok := pushPaths(map[string]any{"on": map[string]any{"push": map[string]any{"branches": []any{"main"}}}}); ok {
		t.Error("a push with no paths filter was read as having one")
	}
}
