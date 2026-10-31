// Package gitenv isolates a test process from the machine's git configuration.
//
// A test that runs git inherits whatever the developer's global and system
// configuration say - commit.gpgsign has already made a fixture report the
// machine rather than the code - so every package that runs git in its tests
// calls Isolate from its TestMain, and tests/go/repo requires it.
package gitenv

import "os"

// Isolate points git's global and system configuration at nothing. The
// repository's own configuration still applies, which is correct: it belongs
// to the repository being examined.
//
// It also clears what binds git to one repository, which is what a hook is
// given. In a linked worktree GIT_DIR is an absolute path and outranks the
// directory a command runs in, so every fixture a pre-push guard made
// committed, branched and wrote its configuration into the real repository,
// bare and a stranger's name included.
func Isolate() {
	_ = os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	_ = os.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	for _, name := range RepositoryLocation {
		_ = os.Unsetenv(name)
	}
}

// RepositoryLocation is every variable that decides which repository git is in:
// what `git rev-parse --local-env-vars` lists, which a test here holds it to,
// and the two that steer the search for one. Each outranks -C and the directory
// a command runs in.
var RepositoryLocation = []string{
	"GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CONFIG", "GIT_CONFIG_PARAMETERS",
	"GIT_CONFIG_COUNT", "GIT_OBJECT_DIRECTORY", "GIT_DIR", "GIT_WORK_TREE",
	"GIT_IMPLICIT_WORK_TREE", "GIT_GRAFT_FILE", "GIT_INDEX_FILE",
	"GIT_NO_REPLACE_OBJECTS", "GIT_REPLACE_REF_BASE", "GIT_PREFIX",
	"GIT_SHALLOW_FILE", "GIT_COMMON_DIR", "GIT_NAMESPACE", "GIT_CEILING_DIRECTORIES",
}

// IsCommit reports whether s is a full commit hash: forty lower-case
// hexadecimal digits. A short hash, a branch and a tag are each a name for
// whatever they point at today, and the things that pin to a commit - a
// site's modules, an action's version - must not accept one.
func IsCommit(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// RemoteMain is where a clone keeps what it last saw of main on its remote.
// Programs that publish ask whether a commit is on main by it, and their
// tests say what main holds by writing it.
const RemoteMain = "refs/remotes/origin/main"
