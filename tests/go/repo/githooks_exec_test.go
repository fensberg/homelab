package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The three git hook shims, run for real.
//
// WHY THEY ARE RUN RATHER THAN READ. These files were on
// tests/coverage-blocklist.yml as #289, #290 and #291 - shipped, load-bearing,
// and executed by nothing. Every test that mentioned them grepped their source,
// which is a change detector: it passes forever while the behaviour rots. Each
// of the three has already been wrong in a way reading would not have caught.
//
//   - githooks/commit-msg did not exist at all for a period, so two commitlint
//     rules this repository holds deliberately were enforced only by CI.
//   - githooks/pre-commit's whole value is ORDERING - the supplier guard has to
//     run before pre-commit clones and installs seven third-party repositories.
//     A test that sees both lines present cannot see which ran first.
//   - githooks/pre-push once used `pre-commit run --hook-stage pre-push`, which
//     does not receive the ref information git supplies, and the push guard
//     refused every push as a result.
//
// HOW. A throwaway repository with a stub `pre-commit` on PATH and a stub
// security program in place of the real one, both appending to one log file.
// The log is the evidence: what ran, in what order, with which arguments. No
// network, no vault, no third-party install.
//
// covers: shell:githooks/commit-msg
// covers: shell:githooks/pre-commit
// covers: shell:githooks/pre-push

// hookFixture is a repository a shim can run inside, plus the log its stubs
// write to.
type hookFixture struct {
	dir string
	log string
	env []string
}

// newHookFixture builds the throwaway repository.
//
// guardExit is what the stand-in for `security guard-deliveries` exits with, so
// a test can ask what the shim does when the guard refuses.
func newHookFixture(t *testing.T, guardExit int) *hookFixture {
	t.Helper()
	dir := t.TempDir()
	// The shims cd to `git rev-parse --show-toplevel`, which is the resolved
	// path. Comparing against an unresolved TempDir fails on any machine where
	// the temp directory is a symlink.
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolving %s: %v", dir, err)
	}
	dir = resolved
	log := filepath.Join(dir, "ran.log")

	binDir := filepath.Join(dir, "bin")
	for _, d := range []string{binDir, filepath.Join(dir, "scripts", "security"), filepath.Join(dir, "toolshed")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("creating %s: %v", d, err)
		}
	}

	env := []string{
		"PATH=" + binDir + ":" + os.Getenv("PATH"),
		"HOME=" + dir,
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"HOOK_LOG=" + log,
		// go build needs somewhere to cache; HOME is the temp dir.
		"GOFLAGS=-mod=mod",
		"GOCACHE=" + filepath.Join(dir, "gocache"),
		"GOMODCACHE=" + filepath.Join(dir, "gomodcache"),
		"GOPATH=" + filepath.Join(dir, "gopath"),
	}

	write := func(path, body string, mode os.FileMode) {
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
	}

	// A stub pre-commit that records the arguments it was handed and anything
	// on stdin. Both matter: git passes the message file as an argument at
	// commit-msg, and the ref information on stdin at pre-push.
	write(filepath.Join(binDir, "pre-commit"), `#!/usr/bin/env bash
{
  echo "pre-commit $*"
  while read -r line; do echo "stdin: $line"; done
} >> "$HOOK_LOG"
exit 0
`, 0o755)

	// The stand-in for scripts/security. The shim builds and runs it, so it has
	// to be a real Go program - and a zero-dependency one, so the build needs no
	// network.
	write(filepath.Join(dir, "scripts", "security", "go.mod"), "module stubsecurity\n\ngo 1.26\n", 0o644)
	write(filepath.Join(dir, "scripts", "security", "main.go"), `package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	f, err := os.OpenFile(os.Getenv("HOOK_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		panic(err)
	}
	fmt.Fprintf(f, "security %s\n", strings.Join(os.Args[1:], " "))
	f.Close()
	os.Exit(`+itoa(guardExit)+`)
}
`, 0o644)

	write(filepath.Join(dir, ".pre-commit-config.yaml"), "repos: []\n", 0o644)

	initRepo := exec.Command("git", "init", "-q", dir)
	initRepo.Env = env
	if out, err := initRepo.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	return &hookFixture{dir: dir, log: log, env: env}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	return string(rune('0' + n))
}

// isolatePATH narrows the fixture's PATH to its own bin directory, linking in
// only the tools the shim genuinely needs.
//
// Removing the stub is not enough to make a command absent: pre-commit is
// installed at /usr/bin on any machine set up by install-dependencies.sh, which
// is the same directory git and bash come from. A test that "removed
// pre-commit" while leaving /usr/bin on PATH would silently exercise the real
// one and prove nothing about the fallback.
func (f *hookFixture) isolatePATH(t *testing.T, tools ...string) {
	t.Helper()
	binDir := filepath.Join(f.dir, "bin")
	for _, tool := range tools {
		target, err := exec.LookPath(tool)
		if err != nil {
			// A failure, not a skip. These are bash, env and git - if one of
			// them is absent the whole suite is meaningless, and skipping
			// would report that as a passing guard.
			t.Fatalf(`%s is not on PATH, so this fixture cannot be built and the hook
shims are not being tested at all.

Skipping here would report an untested shim as a passing one.`, tool)
		}
		if err := os.Symlink(target, filepath.Join(binDir, tool)); err != nil && !os.IsExist(err) {
			t.Fatalf("linking %s: %v", tool, err)
		}
	}
	for i, kv := range f.env {
		if strings.HasPrefix(kv, "PATH=") {
			f.env[i] = "PATH=" + binDir
		}
	}
	if _, err := os.Stat(filepath.Join(binDir, "pre-commit")); err == nil {
		if err := os.Remove(filepath.Join(binDir, "pre-commit")); err != nil {
			t.Fatalf("removing the pre-commit stub: %v", err)
		}
	}
}

// ran returns the log the stubs wrote, or "" if neither ever ran.
func (f *hookFixture) ran(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(f.log)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatalf("reading the stub log: %v", err)
	}
	return string(body)
}

// The supplier guard runs before pre-commit is reached, on both shims that
// carry it.
//
// This is the property the whole interception point exists for, and it is the
// one a source read cannot check: pre-commit installs and executes third-party
// environments before it runs any hook, so a guard that runs second is a
// receipt rather than a refusal.
func TestTheHookShimsRunTheGuardBeforeAnythingThirdParty(t *testing.T) {
	heavy(t, "builds and runs each hook shim for real, seconds apiece")
	root := repoRoot(t)
	for _, hook := range []string{"pre-commit", "pre-push"} {
		t.Run(hook, func(t *testing.T) {
			f := newHookFixture(t, 0)

			var cmd *exec.Cmd
			switch hook {
			case "pre-commit":
				cmd = exec.Command("bash", root+"/githooks/pre-commit")
			case "pre-push":
				cmd = exec.Command("bash", root+"/githooks/pre-push", "origin", "https://example.invalid/r.git")
				cmd.Stdin = strings.NewReader("refs/heads/x 0000 refs/heads/x 1111\n")
			}
			cmd.Dir = f.dir
			cmd.Env = f.env
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("the shim exited %v, so nothing downstream of it ran\n%s", err, out)
			}

			log := f.ran(t)
			guard := strings.Index(log, "security guard-deliveries")
			precommit := strings.Index(log, "pre-commit ")
			if guard < 0 {
				t.Fatalf("the guard never ran:\n%s", log)
			}
			if precommit < 0 {
				t.Fatalf("pre-commit was never reached, so no hook ran at all:\n%s", log)
			}
			if guard > precommit {
				t.Errorf(`githooks/%s reached pre-commit before the guard:

%s
By then the clone and the install have already executed on the machine holding
the vault session, which is the whole thing this ordering exists to prevent.`, hook, log)
			}
		})
	}
}

// A refusing guard stops the commit, and pre-commit is never reached.
//
// `set -e` is what makes this true, and `set -e` is exactly the line somebody
// removes while debugging. Reading for it is a change detector; running a
// failing guard is the property.
func TestAFailingGuardStopsTheHookBeforePreCommit(t *testing.T) {
	heavy(t, "builds and runs each hook shim for real, seconds apiece")
	root := repoRoot(t)
	for _, hook := range []string{"pre-commit", "pre-push"} {
		t.Run(hook, func(t *testing.T) {
			f := newHookFixture(t, 1)

			cmd := exec.Command("bash", root+"/githooks/"+hook)
			cmd.Dir = f.dir
			cmd.Env = f.env
			cmd.Stdin = strings.NewReader("")
			out, err := cmd.CombinedOutput()

			if err == nil {
				t.Errorf("githooks/%s exited 0 with a refusing guard, so the commit or push "+
					"would proceed anyway\n%s", hook, out)
			}
			if strings.Contains(f.ran(t), "pre-commit ") {
				t.Errorf(`githooks/%s reached pre-commit after the guard refused:

%s
The guard's whole purpose is to stop before anything third-party is installed.`, hook, f.ran(t))
			}
		})
	}
}

// Every shim hands git's own arguments through to pre-commit, and reaches it
// through hook-impl rather than `pre-commit run`.
//
// `pre-commit run --hook-stage pre-push` does not receive the ref information
// git supplies, so the push guard - which fails closed when it cannot tell
// which ref is being updated - refused every push. That was found by breaking
// it. This is the test that would have found it instead.
func TestEveryShimPassesGitsOwnArgumentsThrough(t *testing.T) {
	heavy(t, "builds and runs each hook shim for real, seconds apiece")
	root := repoRoot(t)

	cases := []struct {
		hook     string
		args     []string
		stdin    string
		wantArgs []string
	}{
		{
			hook:     "commit-msg",
			args:     []string{".git/COMMIT_EDITMSG"},
			wantArgs: []string{"--hook-type=commit-msg", ".git/COMMIT_EDITMSG"},
		},
		{
			hook:     "pre-push",
			args:     []string{"origin", "https://example.invalid/r.git"},
			stdin:    "refs/heads/topic aaa refs/heads/topic bbb\n",
			wantArgs: []string{"--hook-type=pre-push", "origin", "https://example.invalid/r.git"},
		},
		{
			hook:     "pre-commit",
			wantArgs: []string{"--hook-type=pre-commit"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.hook, func(t *testing.T) {
			f := newHookFixture(t, 0)

			cmd := exec.Command("bash", append([]string{root + "/githooks/" + tc.hook}, tc.args...)...)
			cmd.Dir = f.dir
			cmd.Env = f.env
			cmd.Stdin = strings.NewReader(tc.stdin)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("the shim exited %v\n%s", err, out)
			}

			log := f.ran(t)
			if !strings.Contains(log, "hook-impl") {
				t.Errorf(`githooks/%s does not reach pre-commit through hook-impl:

%s
`+"`pre-commit run --hook-stage <x>`"+` does not receive what git passes, which is
how the push guard came to refuse every push.`, tc.hook, log)
			}
			for _, want := range tc.wantArgs {
				if !strings.Contains(log, want) {
					t.Errorf("githooks/%s did not pass %q through to pre-commit:\n%s", tc.hook, want, log)
				}
			}
			if tc.stdin != "" && !strings.Contains(log, "stdin: refs/heads/topic") {
				t.Errorf(`githooks/%s did not pass git's ref information on stdin:

%s
That is what tells the push guard which ref is being updated, and it fails
closed without it.`, tc.hook, log)
			}
		})
	}
}

// A delivered pre-commit is found even when ~/.local/bin is not on PATH, and
// an absent one says how to get it.
//
// scripts/take-delivery.sh links pre-commit into ~/.local/bin (#416, #423),
// and Debian's ~/.profile adds that directory only if it existed when the
// session started - so the first shell after setup does not have it. The shims
// add it themselves. They used to fall back to `python3 -mpre_commit` for the
// same shape of problem (#257); the system Python no longer holds pre-commit,
// and a copy that did would be one nothing locked by hash.
func TestTheShimsFindTheDeliveredPreCommitAndSayWhenThereIsNone(t *testing.T) {
	root := repoRoot(t)

	t.Run("delivered but not on PATH", func(t *testing.T) {
		f := newHookFixture(t, 0)
		f.isolatePATH(t, "bash", "env", "git")
		delivered := filepath.Join(f.dir, ".local", "bin")
		if err := os.MkdirAll(delivered, 0o755); err != nil {
			t.Fatal(err)
		}
		stub := "#!/usr/bin/env bash\necho \"pre-commit $*\" >> \"$HOOK_LOG\"\nexit 0\n"
		if err := os.WriteFile(filepath.Join(delivered, "pre-commit"), []byte(stub), 0o755); err != nil {
			t.Fatalf("writing the delivered pre-commit stub: %v", err)
		}

		cmd := exec.Command("bash", root+"/githooks/commit-msg", ".git/COMMIT_EDITMSG")
		cmd.Dir = f.dir
		cmd.Env = f.env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("the shim exited %v where pre-commit was delivered to ~/.local/bin\n%s", err, out)
		}
		if !strings.Contains(f.ran(t), "hook-impl") {
			t.Errorf(`the shim did not find the pre-commit take-delivery.sh put in ~/.local/bin:

%s
The first shell after setup often lacks that directory on PATH, and every hook
this repository relies on would refuse to run there.`, f.ran(t))
		}
	})

	t.Run("none delivered", func(t *testing.T) {
		f := newHookFixture(t, 0)
		f.isolatePATH(t, "bash", "env", "git")

		cmd := exec.Command("bash", root+"/githooks/commit-msg", ".git/COMMIT_EDITMSG")
		cmd.Dir = f.dir
		cmd.Env = f.env
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatal("the shim exited 0 with no pre-commit available, so every hook this " +
				"repository relies on would be silently skipped")
		}
		if !strings.Contains(string(out), "install-dependencies.sh") {
			t.Errorf(`the shim did not say how to get pre-commit:

%s
"pre-commit: command not found" says what happened; naming the script that takes
delivery of it says what to do about it.`, out)
		}
	})
}
