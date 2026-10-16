package repo

import (
	"path/filepath"
	"regexp"
	"strings"
)

// How the guards that still read workflows in Go find a uses line and tell
// a file GitHub executes. The rule that once lived beside these is a
// configuration rule now (policy/conftest/action_commits.rego).

var (
	usesLine     = regexp.MustCompile(`(?m)^\s*(?:-\s+)?uses:\s*["']?([^\s"'#]+)`)
	pinnedAction = regexp.MustCompile(`^([A-Za-z0-9._-]+/[A-Za-z0-9._-]+)((?:/[^@]+)?)@([0-9a-f]{40})$`)
)

// runnable reports whether GitHub executes a file's `uses:` lines: a workflow
// at the top of .github/workflows, or the metadata of any composite action.
func runnable(rel string) bool {
	base := filepath.Base(rel)
	if base == "action.yml" || base == "action.yaml" {
		return true
	}
	dir := filepath.ToSlash(filepath.Dir(rel))
	return dir == ".github/workflows" &&
		(strings.HasSuffix(base, ".yml") || strings.HasSuffix(base, ".yaml"))
}
