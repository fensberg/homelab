package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Every program is built into one ignored directory.
//
// The taskfile builds rather than using `go run`, deliberately: `go run`'s
// wrapper swallows SIGINT, which orphaned real infrastructure once. The cost of
// that choice used to be a binary beside each program's source and one ignore
// rule per program - individually forgettable, and two were forgotten. The
// canary never had one, and survey's was added after the binary was already
// tracked, so it never took effect and 3.6MB of compiled Go sat on main until a
// check went looking.
//
// One directory cannot be forgotten for a program nobody thought about. These
// assert the arrangement holds rather than trusting that the next person
// follows it.
func TestNothingIsBuiltOutsideTheToolshed(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "taskfile.yml"))
	if err != nil {
		t.Fatalf("reading taskfile.yml: %v", err)
	}

	found := 0
	for _, m := range goBuildTarget.FindAllStringSubmatch(string(body), -1) {
		target := m[1]
		if target == "/dev/null" {
			continue // a build that only proves it compiles
		}
		found++
		if !strings.Contains(target, "toolshed/") {
			t.Errorf("taskfile.yml builds a binary to %q, outside the toolshed.\n\n"+
				"Then it needs its own ignore rule, which is the arrangement that let a "+
				"binary reach main. Build into toolshed/ instead.", target)
		}
	}
	if found == 0 {
		t.Fatal("no builds found in taskfile.yml, so this guards nothing - and passing " +
			"on an empty set is how a check stops mattering")
	}
}

func TestTheToolshedIsIgnored(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot(t), ".gitignore"))
	if err != nil {
		t.Fatalf("reading .gitignore: %v", err)
	}
	if !regexp.MustCompile(`(?m)^toolshed/?$`).MatchString(string(body)) {
		t.Fatal("toolshed/ is not ignored, so every build leaves untracked binaries and " +
			"the next `git add -A` commits them")
	}
}

// The ignore rule stops the next one. This catches one that got in before the
// rule existed - which is exactly what happened to survey.
func TestNoCompiledBinaryIsTracked(t *testing.T) {
	root := repoRoot(t)
	out, err := exec.Command("git", "-C", root, "ls-files").Output()
	if err != nil {
		t.Fatalf("listing tracked files: %v", err)
	}
	for _, path := range strings.Split(string(out), "\n") {
		if path == "" || strings.HasSuffix(path, ".go") || strings.Contains(path, ".") {
			continue
		}
		info, err := os.Stat(filepath.Join(root, path))
		if err != nil || info.IsDir() || info.Size() < 1<<20 {
			continue // a megabyte of extensionless file is the signature
		}
		if info.Mode()&0o111 == 0 {
			continue
		}
		t.Errorf("%s is a tracked executable of %d bytes. A compiled binary changes on "+
			"every build, bloats the history, and cannot be removed from a pushed commit "+
			"because non_fast_forward applies to every branch here.", path, info.Size())
	}
}

// `go build -C <dir> -o <target> .`
var goBuildTarget = regexp.MustCompile(`go build -C \S+ -o (\S+)`)
