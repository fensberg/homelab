package repo

import (
	"go/token"
	"homelab/tests/parsed"
	"path/filepath"
	"slices"
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
// writtenCommands is the commands one file writes down, each where it is.
type writtenCommands struct {
	source   string
	commands []parsed.Command
}

// Files that write Go commands down. Hooks and scripts are included with the
// workflows because a hook entry is exactly as unrunnable as a workflow step,
// and rather less visible when it breaks. Each is read as what it is: a
// script by the shell parser, and a workflow, an action, the taskfile and the
// hook configuration by YAML, each step, task command or hook entry of it by
// the shell parser - so a comment describing a broken invocation, as the one
// above the fixed step in sensitive-paths.yml does deliberately, is no
// command.
func filesThatInvokeGo(t *testing.T) []writtenCommands {
	t.Helper()
	tree := repository(t)
	var out []writtenCommands
	shell := func(source, body string) []parsed.Command {
		commands, err := parsed.WorkflowCommands(source, body)
		if err != nil {
			t.Fatalf("%s writes down a command that is not shell that can be read: %v", source, err)
		}
		return commands
	}
	for _, rel := range tree.Files(t) {
		under := func(dir string) bool { return strings.HasPrefix(rel, dir+"/") }
		if !under(".github/workflows") && !under(".github/actions") && !under("githooks") && !under("scripts") {
			continue
		}
		switch ext := filepath.Ext(rel); {
		case ext == ".sh" || under("githooks"):
			out = append(out, writtenCommands{rel, parsed.Commands(tree.Shell(t, rel))})
		case (ext == ".yml" || ext == ".yaml") && (under(".github/workflows") || under(".github/actions")):
			var doc struct {
				Jobs map[string]struct {
					Steps []struct {
						Run string `yaml:"run"`
					} `yaml:"steps"`
				} `yaml:"jobs"`
				Runs struct {
					Steps []struct {
						Run string `yaml:"run"`
					} `yaml:"steps"`
				} `yaml:"runs"`
			}
			tree.YAMLInto(t, rel, &doc)
			var commands []parsed.Command
			for _, job := range doc.Jobs {
				for _, step := range job.Steps {
					commands = append(commands, shell(rel, step.Run)...)
				}
			}
			for _, step := range doc.Runs.Steps {
				commands = append(commands, shell(rel, step.Run)...)
			}
			out = append(out, writtenCommands{rel, commands})
		}
	}

	var taskfile struct {
		Tasks map[string]struct {
			Cmds []any `yaml:"cmds"`
		} `yaml:"tasks"`
	}
	tree.YAMLInto(t, "taskfile.yml", &taskfile)
	var tasks []parsed.Command
	for _, task := range taskfile.Tasks {
		for _, c := range task.Cmds {
			cmd, _ := c.(string)
			if m, ok := c.(map[string]any); ok {
				cmd, _ = m["cmd"].(string)
			}
			tasks = append(tasks, shell("taskfile.yml", cmd)...)
		}
	}
	out = append(out, writtenCommands{"taskfile.yml", tasks})

	var hooks struct {
		Repos []struct {
			Hooks []struct {
				Entry string `yaml:"entry"`
			} `yaml:"hooks"`
		} `yaml:"repos"`
	}
	tree.YAMLInto(t, ".pre-commit-config.yaml", &hooks)
	var entries []parsed.Command
	for _, repo := range hooks.Repos {
		for _, hook := range repo.Hooks {
			entries = append(entries, shell(".pre-commit-config.yaml", hook.Entry)...)
		}
	}
	out = append(out, writtenCommands{".pre-commit-config.yaml", entries})
	return out
}

// The package argument, if the command names one. Only `.` and `./…` count: a
// flag value such as `-o ../../toolshed/security` is a path but not a package,
// and treating it as one would fail a command that works.
func packageArg(args []string) (string, bool) {
	for i, f := range args {
		if i > 0 && (args[i-1] == "-o" || args[i-1] == "-C") {
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
func firstSegmentExistsAtRoot(tree *parsed.Tree, pkg string) bool {
	rest := strings.TrimPrefix(pkg, "./")
	seg, _, more := strings.Cut(rest, "/")
	if !more || seg == "" || seg == "..." {
		return false
	}
	return tree.IsDir(seg)
}

func hasGoMod(tree *parsed.Tree, dir string) bool {
	return tree.Exists(filepath.ToSlash(filepath.Join(dir, "go.mod")))
}

func TestNoGoCommandResolvesAgainstTheRootlessRepository(t *testing.T) {
	t.Parallel()
	tree := repository(t)

	if hasGoMod(tree, ".") {
		t.Fatal("there is now a go.mod at the repository root. This test exists " +
			"because there was not one, and every judgement it makes rests on that; " +
			"if a root module is deliberate, revisit this rather than deleting it.")
	}

	judged := 0
	for _, file := range filesThatInvokeGo(t) {
		name := file.source
		for _, c := range file.commands {
			if len(c.Words) < 2 || c.Words[0] != "go" || !slices.Contains([]string{"run", "build", "test", "vet"}, c.Words[1]) {
				continue
			}
			sub, args := c.Words[1], c.Words[2:]
			pkg, ok := packageArg(args)
			if !ok {
				continue
			}

			if at := slices.Index(args, "-C"); at >= 0 && at+1 < len(args) {
				dirWord := args[at+1]
				// A discovered directory, not a written one.
				//
				// taskfile.yml sweeps `scripts/*/go.mod` rather than naming
				// each module, because the hand-written lists it replaced went
				// stale - clerk and inspector were on none of them and were
				// never compiled or run by anything. The loop tests for a
				// go.mod before it does anything, so the property this guard
				// exists to enforce is enforced by the shell instead, one line
				// earlier and at run time.
				//
				// Refused rather than skipped when that test is absent: a bare
				// `-C "$dir"` with nothing checking $dir is exactly the
				// unresolvable invocation this was written for.
				if strings.HasPrefix(dirWord, "$") || strings.HasPrefix(dirWord, `"$`) {
					checked := strings.TrimSuffix(dirWord, `"`) + `/go.mod"`
					if !strings.HasPrefix(checked, `"`) {
						checked = `"` + checked
					}
					tests := slices.ContainsFunc(file.commands, func(c parsed.Command) bool {
						return len(c.Words) >= 4 && c.Words[0] == "[" && c.Words[1] == "-f" && c.Words[2] == checked
					})
					if !tests {
						t.Errorf("%s: `go %s -C %s` changes into a directory this cannot resolve, "+
							"and nothing in the file checks that it holds a go.mod first", name, sub, dirWord)
					}
					continue
				}
				judged++
				if !hasGoMod(tree, dirWord) {
					t.Errorf("%s: `go %s -C %s` changes into %s, which has no go.mod, "+
						"so the command has no module to work in", name, sub, dirWord, dirWord)
					continue
				}
				checkMainPackage(t, tree, name, sub, pkg, filepath.ToSlash(filepath.Join(dirWord, pkg)))
				continue
			}

			if !firstSegmentExistsAtRoot(tree, pkg) {
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
func checkMainPackage(t *testing.T, tree *parsed.Tree, name, sub, pkg, pkgDir string) {
	t.Helper()
	if (sub != "run" && sub != "build") || strings.Contains(pkg, "...") {
		return
	}
	if !tree.IsDir(pkgDir) {
		t.Errorf("%s: `go %s` targets %s, which does not exist", name, sub, pkgDir)
		return
	}
	fset := token.NewFileSet()
	for _, rel := range tree.Files(t) {
		if filepath.Dir(rel) == pkgDir && strings.HasSuffix(rel, ".go") && tree.Go(t, fset, rel).Name.Name == "main" {
			return
		}
	}
	t.Errorf("%s: `go %s` targets %s, which has no main package, so there is "+
		"nothing there to run", name, sub, pkgDir)
}
