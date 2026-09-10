package repo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A test package that shells out to git must cut itself off from the
// developer's git configuration.
//
// Two tests in scripts/gatehouse were written without doing so, in consecutive
// changes. One built a deliberately unsigned commit and inherited
// `commit.gpgsign` from a global config, so the fixture was signed and the test
// failed on any machine set up the way this repository tells people to set one
// up. Both reported the machine rather than the code, and one of them surfaced
// on a workstation in the middle of a push.
//
// GIT_CONFIG_GLOBAL and GIT_CONFIG_SYSTEM pointed at /dev/null in a TestMain
// make the fixtures depend on what the tests set and nothing else.
// Per-repository config still applies, which is right - the fixture sets that
// itself.
//
// This is a check rather than a note because the failure is invisible on the
// machine that writes it. It only appears on a differently-configured one,
// which by then is somebody else's afternoon.
func TestPackagesThatShellToGitNeutraliseUserConfig(t *testing.T) {
	root := repoRoot(t)

	// Directories holding Go test packages worth checking.
	roots := []string{
		filepath.Join(root, "scripts"),
		filepath.Join(root, "tests", "go"),
	}

	// package directory -> whether it invokes git, and whether it neutralises
	invokesGit := map[string]bool{}
	neutralises := map[string]bool{}
	visited := 0

	for _, r := range roots {
		err := filepath.Walk(r, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, "_test.go") {
				return err
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			text := string(body)
			dir := filepath.Dir(path)
			visited++

			// Shelling out to git, by any of the shapes used here.
			if strings.Contains(text, `exec.Command("git"`) ||
				strings.Contains(text, `exec.CommandContext(ctx, "git"`) {
				invokesGit[dir] = true
			}
			if strings.Contains(text, "GIT_CONFIG_GLOBAL") {
				neutralises[dir] = true
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("walking %s: %v", r, err)
		}
	}

	// Prove the walk worked before drawing any conclusion from what it found.
	//
	// This used to go straight to `t.Skip("no test package invokes git")`, and
	// a skip is the wrong answer twice over. It reports as neither pass nor
	// fail, so CI treats it as fine; and it cannot tell "no package invokes
	// git" - a real, checkable statement - from "this walk read no test files
	// at all", which means the guard is off. A rename or a move under either
	// root produces the second, permanently and in silence, and this is the
	// guard that catches a failure mode CLAUDE.md records as having already
	// happened twice.
	//
	// With a floor underneath it, an empty invokesGit genuinely means no
	// package shells out to git, which is a pass rather than a skip.
	if visited < 40 {
		t.Fatalf("only %d test files were read under scripts/ and tests/go/, which cannot be right - this guard is walking the wrong tree and proves nothing", visited)
	}
	if len(invokesGit) == 0 {
		return // Checked, and nothing shells out to git.
	}

	for dir := range invokesGit {
		if neutralises[dir] {
			continue
		}
		rel, _ := filepath.Rel(root, dir)
		t.Errorf("%s runs git in its tests and does not neutralise the developer's "+
			"git configuration.\n\n"+
			"Add a TestMain setting GIT_CONFIG_GLOBAL and GIT_CONFIG_SYSTEM to "+
			"os.DevNull. Without it a fixture inherits whatever the machine has - "+
			"commit.gpgsign is the one that has already caused this - and the test "+
			"reports the machine rather than the code.", rel)
	}
}
