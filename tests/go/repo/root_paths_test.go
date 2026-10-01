package repo

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// A root reaches files outside itself from the repository's top.
//
// A file that reads something outside its own directory names it from the
// top of the repository, by climbing exactly as far as its directory is deep.
//
// A plan against the as-built record runs a copy of a root, placed at the
// root's own depth so that its way up still leads to the same file
// (details/asbuilt.copyRoot). Any other way up does not: the copy's parent is
// not management/. That broke the pull-request plan the day the cluster root
// first read management/tunnel-routes.json as "../": the plan refused before
// its first step, on a file that exists. A module is read in place, from
// wherever it is, and the same rule keeps it honest: a path that stops short
// of the top depends on what the module's neighbours happen to be.
//
// Asserted of every OpenTofu file the repository authored, by its own depth,
// so a root or a module at any depth is held to it without being named.
var pathModuleUp = regexp.MustCompile(`\$\{path\.module\}((?:/\.\.)+)`)

func TestAFileReachesOutsideItsDirectoryFromTheTop(t *testing.T) {
	var found []string
	for rel, body := range tofuSources(t) {
		found = append(found, climbsWrong(rel, body)...)
	}
	sort.Strings(found)
	for _, f := range found {
		t.Error(f)
	}
}

// climbsWrong is every path.module reference in one file that leaves its
// directory by other than the number of levels the directory is deep.
func climbsWrong(rel, body string) []string {
	depth := 0
	if dir := filepath.ToSlash(filepath.Dir(rel)); dir != "." {
		depth = strings.Count(dir, "/") + 1
	}
	var out []string
	for i, line := range strings.Split(body, "\n") {
		for _, m := range pathModuleUp.FindAllStringSubmatch(line, -1) {
			if strings.Count(m[1], "/..") != depth {
				out = append(out, fmt.Sprintf("%s:%d reaches outside its directory by %q, and the top of the repository is %d level(s) up. A copy of a root planned against the as-built record sits at the same depth elsewhere, so only the way to the top leads to the same file from there. Write the path from the repository's top.", rel, i+1, "${path.module}"+m[1], depth))
			}
		}
	}
	return out
}

func TestClimbsWrongFindsEveryOtherWayUp(t *testing.T) {
	for _, c := range []struct {
		file, line string
		want       bool
	}{
		{"one/two/a.tf", `file("${path.module}/../tunnel-routes.json")`, true},
		{"one/two/a.tf", `file("${path.module}/../../../x")`, true},
		{"one/two/a.tf", `file("${path.module}/../../management/tunnel-routes.json")`, false},
		{"one/two/a.tf", `file("${path.module}/files/x")`, false},
		// The same climb is right or wrong by how deep the file is.
		{"one/two/three/a.tf", `file("${path.module}/../../../x")`, false},
		{"one/two/three/a.tf", `file("${path.module}/../../x")`, true},
	} {
		if got := len(climbsWrong(c.file, c.line)) > 0; got != c.want {
			t.Errorf("%s in %s: found=%v, want %v", c.line, c.file, got, c.want)
		}
	}
}
