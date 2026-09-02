package repo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The supplier guard has to run before pre-commit, not as one of its hooks.
//
// pre-commit clones a hook repository and installs its environment before it
// runs any hook, and installing executes setup code - npm install, pip
// install, a Go build. A guard that is itself a pre-commit hook therefore runs
// after the code it exists to refuse has already executed on the machine
// holding the vault session. Gating execution is not gating download.
//
// git invokes githooks/pre-commit. Nothing third-party has run at that point.
// So the guard belongs there, and these assert it is there, that it comes
// first, and that git is actually pointed at it - because a shim nothing
// invokes is a file.

func TestTheHookShimRunsTheGuardBeforePreCommit(t *testing.T) {
	for _, hook := range []string{"pre-commit", "pre-push"} {
		body := readShim(t, hook)

		guard := strings.Index(body, "supplierguard")
		precommit := strings.Index(body, "exec pre-commit")

		if guard < 0 {
			t.Errorf("githooks/%s does not run the supplier guard, so an unapproved "+
				"repository would be cloned and installed before anything checked it", hook)
			continue
		}
		if precommit < 0 {
			t.Errorf("githooks/%s never reaches pre-commit, so no hook runs at all", hook)
			continue
		}
		if guard > precommit {
			t.Errorf("githooks/%s runs the guard after pre-commit. By then the clone and "+
				"the install have happened, which is the whole thing this ordering "+
				"exists to prevent.", hook)
		}
	}
}

// A shim that exits zero on a failing guard is a shim that reports rather than
// refuses. `set -e` is what makes the guard's exit code stop the commit.
func TestTheHookShimFailsOnAFailingGuard(t *testing.T) {
	for _, hook := range []string{"pre-commit", "pre-push"} {
		body := readShim(t, hook)
		if !strings.Contains(body, "set -euo pipefail") {
			t.Errorf("githooks/%s does not set -e, so a failing guard would be reported "+
				"and the commit would proceed anyway", hook)
		}
	}
}

// git will not run a shim it cannot execute, and will not say so loudly.
func TestTheHookShimsAreExecutable(t *testing.T) {
	for _, hook := range []string{"pre-commit", "pre-push"} {
		info, err := os.Stat(filepath.Join(repoRoot(t), "githooks", hook))
		if err != nil {
			t.Fatalf("githooks/%s: %v", hook, err)
		}
		if info.Mode()&0o111 == 0 {
			t.Errorf("githooks/%s is not executable, so git silently skips it and every "+
				"hook stops running", hook)
		}
	}
}

// The shim only guards anything if git is pointed at it. Left unset, git uses
// .git/hooks - which pre-commit owns and overwrites - and the guard is a file
// nobody runs.
func TestSetupPointsGitAtTheShims(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "install-dependencies.sh"))
	if err != nil {
		t.Fatalf("reading install-dependencies.sh: %v", err)
	}
	s := string(body)
	if !strings.Contains(s, "core.hooksPath githooks") {
		t.Fatal("setup does not point git at githooks/, so the shims are files nobody " +
			"runs and pre-commit's own hook is what git invokes")
	}
	if strings.Contains(s, "pre-commit install\n") {
		t.Error("setup still runs `pre-commit install`, which writes .git/hooks/pre-commit " +
			"and takes precedence over nothing - but it is the mechanism the shim " +
			"replaces, and leaving both means whichever git picks decides whether the " +
			"guard runs")
	}
}

func readShim(t *testing.T, hook string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot(t), "githooks", hook))
	if err != nil {
		t.Fatalf("reading githooks/%s: %v", hook, err)
	}
	return string(body)
}
