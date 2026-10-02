package repo

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"homelab/details/applications"
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
	root := repoRoot(t)
	apps, err := applications.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range apps {
		if err := runApplicationTests(t, root, a); err != nil {
			t.Error(err)
		}
	}
}

// runApplicationTests vets and runs one application's own tests, and says how
// they failed. An application with no tests has none to fail.
func runApplicationTests(t *testing.T, root string, a applications.Application) error {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(a.Root), applicationTestsDir)
	_, err := os.Stat(filepath.Join(dir, "go.mod"))
	switch {
	case os.IsNotExist(err):
		tests, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
		if err != nil {
			return err
		}
		if len(tests) > 0 {
			return fmt.Errorf("%s/%s holds tests and no go.mod, so nothing can run them. An application's tests are a Go module of their own.", a.Root, applicationTestsDir)
		}
		return nil
	case err != nil:
		return err
	}
	manifests, err := applicationManifests(t, root, a)
	if err != nil {
		return fmt.Errorf("%s: its manifests could not be read for its tests: %w", a.Name, err)
	}
	for _, args := range [][]string{{"vet", "./..."}, {"test", "-count=1", "./..."}} {
		cmd := exec.Command("go", args...)
		cmd.Dir = dir
		// The module is the application's own and nothing else's: no
		// workspace above it and no flags inherited from whoever runs this.
		cmd.Env = append(os.Environ(), applications.ManifestsEnv+"="+manifests, "GOWORK=off", "GOFLAGS=")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%s's own tests do not pass (go %s in %s/%s):\n\n%s", a.Name, strings.Join(args, " "), a.Root, applicationTestsDir, out)
		}
	}
	return nil
}

// applicationManifests writes every YAML file of an application as JSON, and
// returns the directory holding them.
func applicationManifests(t *testing.T, root string, a applications.Application) (string, error) {
	t.Helper()
	out := t.TempDir()
	base := filepath.Join(root, filepath.FromSlash(a.Root))
	var found []string
	err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if ext := filepath.Ext(path); ext == ".yaml" || ext == ".yml" {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(found)
	for _, path := range found {
		body, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		docs := []any{}
		dec := yaml.NewDecoder(strings.NewReader(string(body)))
		for {
			var doc any
			if err := dec.Decode(&doc); err == io.EOF {
				break
			} else if err != nil {
				return "", fmt.Errorf("%s: %w", path, err)
			}
			if doc != nil {
				docs = append(docs, doc)
			}
		}
		encoded, err := json.Marshal(docs)
		if err != nil {
			return "", fmt.Errorf("%s: %w", path, err)
		}
		rel, _ := filepath.Rel(base, path)
		dst := filepath.Join(out, strings.TrimSuffix(rel, filepath.Ext(rel))+".json")
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(dst, encoded, 0o644); err != nil {
			return "", err
		}
	}
	return out, nil
}

// The runner is held to what it claims, against applications written here: it
// runs a module that passes and says nothing, reports one that fails with what
// the failing test printed, reports tests nothing could run, hands each module
// its application's manifests, and passes over an application with no tests.
func TestRunApplicationTestsReportsExactlyTheFailing(t *testing.T) {
	heavy(t, "compiles three small test modules")
	root := t.TempDir()
	write := func(rel, body string) {
		path := filepath.Join(root, filepath.FromSlash(applications.Dir), filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
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

	apps, err := applications.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	// What each application's tests came to, "<nil>" for those that passed
	// or had none.
	got := map[string]string{}
	failed := 0
	for _, a := range apps {
		got[a.Name] = fmt.Sprint(runApplicationTests(t, root, a))
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
