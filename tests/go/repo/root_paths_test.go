package repo

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// A root reaches files outside itself from the repository's top.
//
// A plan against the as-built record runs a copy of the cluster root, placed
// at the root's own depth so that "${path.module}/../../" still leads to the
// same file (details/asbuilt.copyRoot). Any other way up does not: the copy's
// parent is not management/. That broke the pull-request plan the day the
// cluster root first read management/tunnel-routes.json as "../": the plan
// refused before its first step, on a file that exists.
//
// Asserted of every .tf file directly in an OpenTofu root under management/:
// a path.module reference that leaves the root climbs exactly two levels.
var pathModuleUp = regexp.MustCompile(`\$\{path\.module\}((?:/\.\.)+)`)

func TestARootReachesOutsideItselfFromTheTop(t *testing.T) {
	root := repoRoot(t)
	files, err := filepath.Glob(filepath.Join(root, "management", "*", "*.tf"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 10 {
		t.Fatalf("only %d root files were found, so the enumeration has stopped matching", len(files))
	}
	var found []string
	for _, f := range files {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(root, f)
		found = append(found, climbsWrong(rel, string(body))...)
	}
	sort.Strings(found)
	for _, f := range found {
		t.Error(f)
	}
}

// climbsWrong is every path.module reference in one file that leaves the root
// by other than two levels.
func climbsWrong(rel, body string) []string {
	var out []string
	for i, line := range strings.Split(body, "\n") {
		for _, m := range pathModuleUp.FindAllStringSubmatch(line, -1) {
			if strings.Count(m[1], "/..") != 2 {
				out = append(out, fmt.Sprintf("%s:%d reaches outside its root by %q. A copy of the root planned against the as-built record sits at the same depth elsewhere, so only \"${path.module}/../../\" leads to the same file from there. Write the path from the repository's top.", rel, i+1, "${path.module}"+m[1]))
			}
		}
	}
	return out
}

func TestClimbsWrongFindsEveryOtherWayUp(t *testing.T) {
	for _, c := range []struct {
		line string
		want bool
	}{
		{`file("${path.module}/../tunnel-routes.json")`, true},
		{`file("${path.module}/../../../x")`, true},
		{`file("${path.module}/../../management/tunnel-routes.json")`, false},
		{`file("${path.module}/files/x")`, false},
	} {
		if got := len(climbsWrong("a.tf", c.line)) > 0; got != c.want {
			t.Errorf("%s: found=%v, want %v", c.line, got, c.want)
		}
	}
}
