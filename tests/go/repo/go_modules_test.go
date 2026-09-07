package repo

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Every Go module in scripts/ is vetted, built and tested by the taskfile.
//
// It was not, and the way that went unnoticed is the point. `scripts/clerk`
// and `scripts/inspector` were absent from both `validate` and `test:go`, so
// neither was compiled or run by anything - not locally, not in CI. Guards
// written for the clerk had passed only in the terminal that wrote them and
// would have gone on passing after the code they guard stopped working,
// because nothing ever ran them again.
//
// That is the failure this repository refuses everywhere else: a check that is
// absent looks exactly like a check that is fine. A new module is added by
// creating a directory with a go.mod in it, and nothing about that act
// suggests two lists elsewhere need editing - so the lists are asserted here
// rather than remembered.
//
// Deliberately checked against the taskfile rather than against the workflow:
// CI runs `task validate` and `task test`, so the taskfile is where the
// contract lives, and the agent cannot write workflows anyway.
func TestEveryGoModuleIsValidatedAndTested(t *testing.T) {
	root := repoRoot(t)

	entries, err := os.ReadDir(filepath.Join(root, "scripts"))
	if err != nil {
		t.Fatalf("reading scripts/: %v", err)
	}
	var modules []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, "scripts", e.Name(), "go.mod")); err == nil {
			modules = append(modules, e.Name())
		}
	}
	if len(modules) == 0 {
		t.Fatal("no Go module found under scripts/, so this asserts nothing")
	}
	sort.Strings(modules)

	taskfile := readRepoFile(t, "taskfile.yml")

	for _, m := range modules {
		t.Run(m, func(t *testing.T) {
			for _, want := range []struct {
				line, why string
			}{
				{
					"go vet -C scripts/" + m + " ./...",
					"nothing type-checks it, so a compile error reaches a user rather than a build",
				},
				{
					"go build -C scripts/" + m,
					"nothing links it, so a broken import is found by whoever runs it first",
				},
				{
					"go test -C scripts/" + m + " ./...",
					"nothing runs its tests, so every guard in it is dead and still looks green",
				},
			} {
				if !strings.Contains(taskfile, want.line) {
					t.Errorf(`taskfile.yml never runs %q.

scripts/%s is a Go module and %s.

Add it to `+"`validate`"+` and `+"`test:go`"+` beside the others. This is not
bookkeeping: clerk and inspector sat outside both lists for their whole lives,
so their tests had never run anywhere but the terminal that wrote them.`,
						want.line, m, want.why)
				}
			}
		})
	}
}
