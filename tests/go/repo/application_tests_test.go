package repo

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homelab/details/applications"
	"homelab/tests/parsed"
	"path"
)

// An application's own tests are run with everything else.
//
// What is true of one application and of nothing else is tested in that
// application's directory, so that removing the application removes its tests
// with it (#590): a test of it kept here would be a file outside its
// directory that has to go too. Each application that has tests keeps them as
// a Go module of its own under tests/, with no dependency - and this runs
// every one of them, so a test written there is in the same lane as a guard
// written here and a failing one stops the same pull request.
//
// Found by walking the applications, so the next one's tests are run the day
// they are written. An estate with no application runs none, and that is not
// this guard failing to look: the runner is proved against applications
// written here, below, whichever the estate has.
//
// Each module is handed the application's manifests as JSON, in the directory
// applications.ManifestsEnv names: every YAML file of the application as an array
// of its documents, at the same path with .json for its extension. That is
// what lets an application's tests read its manifests with the standard
// library alone.
const applicationTestsDir = "tests"

func TestEveryApplicationsOwnTestsPass(t *testing.T) {
	heavy(t, "compiles and runs each application's own test module, whole seconds apiece")
	tree := repository(t)
	for _, a := range tree.Applications(t) {
		if err := runApplicationTests(t, tree, a); err != nil {
			t.Error(err)
		}
	}
}

// runApplicationTests vets and runs one application's own tests, and says how
// they failed. An application with no tests has none to fail.
func runApplicationTests(t *testing.T, tree *parsed.Tree, a applications.Application) error {
	t.Helper()
	dir := a.Root + "/" + applicationTestsDir
	if !tree.Exists(dir + "/go.mod") {
		for _, rel := range tree.Files(t) {
			if path.Dir(rel) == dir && strings.HasSuffix(rel, "_test.go") {
				return fmt.Errorf("%s holds tests and no go.mod, so nothing can run them. An application's tests are a Go module of their own.", dir)
			}
		}
		return nil
	}
	manifests := applicationManifests(t, tree, a)
	for _, args := range [][]string{{"vet", "./..."}, {"test", "-count=1", "./..."}} {
		// The module is the application's own and nothing else's: no
		// workspace above it and no flags inherited from whoever runs this.
		stdout, stderr, err := tree.Invoke(parsed.Run{Dir: dir, Env: []string{applications.ManifestsEnv + "=" + manifests, "GOWORK=off", "GOFLAGS="}}, "go", args...)
		if err != nil {
			return fmt.Errorf("%s's own tests do not pass (go %s in %s):\n\n%s%s", a.Name, strings.Join(args, " "), dir, stdout, stderr)
		}
	}
	return nil
}

// applicationManifests writes every YAML file of an application as JSON, and
// returns the directory holding them: a directory of the test's own, so what
// the application's tests are handed is a copy and never the tree.
func applicationManifests(t *testing.T, tree *parsed.Tree, a applications.Application) string {
	t.Helper()
	out := t.TempDir()
	for _, rel := range tree.Files(t) {
		inside, ok := strings.CutPrefix(rel, a.Root+"/")
		if ext := path.Ext(rel); !ok || (ext != ".yaml" && ext != ".yml") {
			continue
		}
		encoded, err := json.Marshal(append([]any{}, tree.YAMLDocuments(t, rel)...))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		dst := filepath.Join(out, filepath.FromSlash(strings.TrimSuffix(inside, path.Ext(inside))+".json"))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, encoded, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// The runner is held to what it claims, against applications written here: it
// runs a module that passes and says nothing, reports one that fails with what
// the failing test printed, reports tests nothing could run, hands each module
// its application's manifests, and passes over an application with no tests.
func TestRunApplicationTestsReportsExactlyTheFailing(t *testing.T) {
	heavy(t, "compiles three small test modules")
	tree := parsed.Empty(t)
	write := func(rel, body string) {
		tree.Write(t, applications.Dir+"/"+rel, body)
	}
	module := func(app, test string) {
		write(app+"/"+applications.Declaration, "{}")
		write(app+"/tests/go.mod", "module example/"+app+"\n\ngo 1.26\n")
		write(app+"/tests/it_test.go", "package tests\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\n"+test)
	}
	module("passes", "func TestIt(t *testing.T) {\n\tbody, err := os.ReadFile(os.Getenv(\""+applications.ManifestsEnv+"\") + \"/base/thing.json\")\n\tif err != nil || string(body) != `[{\"kind\":\"A\"},{\"kind\":\"B\"}]` {\n\t\tt.Fatalf(\"manifests: %q, %v\", body, err)\n\t}\n}\n")
	write("passes/base/thing.yaml", "kind: A\n---\nkind: B\n")
	module("fails", "func TestIt(t *testing.T) {\n\t_ = os.Args\n\tt.Fatal(\"the reason it failed\")\n}\n")
	write("quiet/"+applications.Declaration, "{}")
	write("unrunnable/"+applications.Declaration, "{}")
	write("unrunnable/tests/it_test.go", "package tests\n")

	tree.Index(t)
	apps := tree.Applications(t)
	// What each application's tests came to, "<nil>" for those that passed
	// or had none.
	got := map[string]string{}
	failed := 0
	for _, a := range apps {
		got[a.Name] = fmt.Sprint(runApplicationTests(t, tree, a))
		if got[a.Name] != "<nil>" {
			failed++
		}
	}
	if len(got) != 4 || failed != 2 || got["passes"] != "<nil>" || got["quiet"] != "<nil>" {
		t.Fatalf("want four applications of which two fail, got: %v", got)
	}
	if !strings.Contains(got["fails"], "fails's own tests do not pass") || !strings.Contains(got["fails"], "the reason it failed") {
		t.Errorf("the failing module was reported as:\n%s", got["fails"])
	}
	if !strings.Contains(got["unrunnable"], "no go.mod") {
		t.Errorf("tests nothing could run were reported as:\n%s", got["unrunnable"])
	}
}
