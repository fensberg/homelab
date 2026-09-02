package repo

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A Go command written down in this repository must be one that can run.
//
// There is no module at the repository root, deliberately: each program under
// scripts/ is its own module, so one program's dependency is not every
// program's. The consequence is easy to forget - `go run ./scripts/foo` has no
// main module to resolve that path against, and fails with "cannot find main
// module" every single time, on every machine.
//
// That shipped. The sensitive-paths workflow ran `go run ./scripts/attestation`
// and the gate on every sensitive change failed on its first real pull request
// with a Go error instead of a verdict. The test meant to keep the workflow
// wired asserted the file CONTAINED that string, so it was green throughout,
// describing an invocation that had never once worked. A test matching the
// spelling of a command cannot tell you the command is broken; only resolving
// what it names can.
//
// Scope, stated because a guard whose edges nobody knows is worse than none.
// Which module a package path resolves against is decided by the working
// directory, and that can come from a step's `working-directory:`, a task's
// `dir:`, or a `cd` inside a shell loop - none of which this can see. So it
// judges the two forms whose directory is not in doubt: `-C dir`, which settles
// it outright, and a package path whose first segment is a directory at the
// repository root, which is therefore being resolved from the root. Forms that
// depend on context (`.`, `./...`, `./api/...`) are skipped rather than
// guessed at.
var (
	goInvocation = regexp.MustCompile(`\bgo\s+(run|build|test|vet)\s+([^\n]*)`)
	goChangeDir  = regexp.MustCompile(`-C\s+(\S+)`)
	packageClaus = regexp.MustCompile(`(?m)^package\s+(\w+)`)
)

// Files that write Go commands down. Hooks and scripts are included with the
// workflows because a hook entry is exactly as unrunnable as a workflow step,
// and rather less visible when it breaks.
func filesThatInvokeGo(t *testing.T) map[string]string {
	t.Helper()
	root := repoRoot(t)

	var paths []string
	for _, dir := range []string{".github/workflows", ".github/actions", "githooks", "scripts"} {
		_ = filepath.WalkDir(filepath.Join(root, dir), func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			switch filepath.Ext(p) {
			case ".yml", ".yaml", ".sh", "":
				paths = append(paths, p)
			}
			return nil
		})
	}
	paths = append(paths,
		filepath.Join(root, "taskfile.yml"),
		filepath.Join(root, ".pre-commit-config.yaml"),
	)

	out := make(map[string]string, len(paths))
	for _, p := range paths {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			rel = p
		}
		// A workflow is read as it will be once any outstanding patch is
		// applied, for the reason patches_test.go gives: the agent cannot
		// write one, so the fix arrives as a patch, and this test would
		// otherwise be red for the whole hand-over window and block it.
		if filepath.Dir(rel) == filepath.Join(".github", "workflows") {
			out[rel] = intendedWorkflow(t, filepath.Base(rel))
			continue
		}
		body, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		out[rel] = string(body)
	}
	if len(out) == 0 {
		t.Fatal("found no files to scan; this test is checking nothing")
	}
	return out
}

// The package argument, if the command names one. Only `.` and `./…` count: a
// flag value such as `-o ../../toolshed/security` is a path but not a package,
// and treating it as one would fail a command that works.
func packageArg(args string) (string, bool) {
	fields := strings.Fields(args)
	for i, f := range fields {
		if i > 0 && (fields[i-1] == "-o" || fields[i-1] == "-C") {
			continue
		}
		if f == "." || strings.HasPrefix(f, "./") {
			return f, true
		}
	}
	return "", false
}

// A package path is root-relative when its first segment is a directory that
// exists at the repository root. `./scripts/attestation` is; `./api/...`,
// whose first segment exists only under tests/go, is not - that one is
// relative to a working directory this cannot see, so it is left alone.
func firstSegmentExistsAtRoot(root, pkg string) bool {
	rest := strings.TrimPrefix(pkg, "./")
	seg, _, more := strings.Cut(rest, "/")
	if !more || seg == "" || seg == "..." {
		return false
	}
	info, err := os.Stat(filepath.Join(root, seg))
	return err == nil && info.IsDir()
}

func hasGoMod(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "go.mod"))
	return err == nil
}

func TestNoGoCommandResolvesAgainstTheRootlessRepository(t *testing.T) {
	root := repoRoot(t)

	if hasGoMod(root) {
		t.Fatal("there is now a go.mod at the repository root. This test exists " +
			"because there was not one, and every judgement it makes rests on that; " +
			"if a root module is deliberate, revisit this rather than deleting it.")
	}

	judged := 0
	for name, body := range filesThatInvokeGo(t) {
		for _, line := range strings.Split(body, "\n") {
			trimmed := strings.TrimSpace(line)
			// A comment may describe a broken invocation deliberately - the
			// one above the fixed step in sensitive-paths.yml does exactly
			// that, to say why the form was wrong.
			if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") {
				continue
			}
			m := goInvocation.FindStringSubmatch(trimmed)
			if m == nil {
				continue
			}
			sub, args := m[1], m[2]
			pkg, ok := packageArg(args)
			if !ok {
				continue
			}

			if c := goChangeDir.FindStringSubmatch(args); c != nil {
				judged++
				dir := filepath.Join(root, c[1])
				if !hasGoMod(dir) {
					t.Errorf("%s: `go %s -C %s` changes into %s, which has no go.mod, "+
						"so the command has no module to work in", name, sub, c[1], c[1])
					continue
				}
				checkMainPackage(t, root, name, sub, pkg, filepath.Join(dir, pkg))
				continue
			}

			if !firstSegmentExistsAtRoot(root, pkg) {
				continue // relative to a working directory this cannot see
			}
			judged++

			t.Errorf("%s: `go %s %s` resolves that path from the repository root, "+
				"where there is no go.mod.\n\n"+
				"Every program here is its own module, so there is no main module at "+
				"the root for a ./… path to resolve against: this command fails with "+
				"\"cannot find main module\" every time it runs, on every machine.\n\n"+
				"Write it as `go %s -C %s .` instead.",
				name, sub, pkg, sub, strings.TrimPrefix(pkg, "./"))
		}
	}

	if judged == 0 {
		t.Fatal("this test judged no Go command at all. Either every -C invocation " +
			"has been rewritten or the pattern no longer matches how they are " +
			"written, and it is now passing without looking at anything.")
	}
}

// `go run` and `go build` need something with a main package to run. Checked
// only where the directory is known, which is the -C form.
func checkMainPackage(t *testing.T, root, name, sub, pkg, pkgDir string) {
	t.Helper()
	if (sub != "run" && sub != "build") || strings.Contains(pkg, "...") {
		return
	}
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		t.Errorf("%s: `go %s` targets %s, which does not exist",
			name, sub, strings.TrimPrefix(pkgDir, root+"/"))
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(pkgDir, e.Name()))
		if err != nil {
			continue
		}
		if p := packageClaus.FindSubmatch(src); p != nil && string(p[1]) == "main" {
			return
		}
	}
	t.Errorf("%s: `go %s` targets %s, which has no main package, so there is "+
		"nothing there to run", name, sub, strings.TrimPrefix(pkgDir, root+"/"))
}
