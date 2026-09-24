// Package repopath answers where this repository is: on disk, and on GitHub.
//
// It exists because the answer was copied eight times, across two modules,
// each copy counting its own way up with a fixed chain of "..". Every copy was
// correct until the file holding it moved - and the move that proved it was
// the one that let the test tiers share the contractor's config reader, which
// broke four tests at once by changing a depth by one.
//
// So the depth is not counted. Root walks upward from this package's own
// source file until it finds the marker, which holds wherever the caller
// lives and wherever `go test` was started from.
package repopath

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Marker is the file that identifies the repository root. The existing checks
// already treated CLAUDE.md as the sign they had found the right directory, so
// this keeps the same answer rather than introducing a second one.
const Marker = "CLAUDE.md"

// Root returns the repository root.
//
// It starts from this file rather than the working directory, so it gives the
// same answer however the tests were invoked. Only meaningful in a source
// checkout, which is the only place anything calls it: tests read the
// repository's own files.
func Root() (string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("could not determine where repopath's own source file is")
	}
	return rootFrom(filepath.Dir(thisFile))
}

// rootFrom walks up from dir until it finds Marker.
func rootFrom(dir string) (string, error) {
	for {
		if _, err := os.Stat(filepath.Join(dir, Marker)); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("walked to the filesystem root without finding " + Marker +
				" - this is not running from a checkout of the repository")
		}
		dir = parent
	}
}

// Join is Root followed by filepath.Join, for the common case of wanting one
// path inside the repository.
func Join(parts ...string) (string, error) {
	root, err := Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{root}, parts...)...), nil
}

// Slug is the repository's owner/name on GitHub.
//
// GITHUB_REPOSITORY in Actions, which is authoritative there; the origin
// remote on a workstation. Read rather than written down, because this
// repository is meant to be forked, and a name written into the code is the
// name of the estate it was copied from. The api tier's ruleset check had
// exactly that: a fork running it would have inspected the original's rules.
//
// Moved here from the contractor's internal phases, where it could not be
// reached by anything else that needed it.
func Slug() (string, error) {
	if s := strings.TrimSpace(os.Getenv("GITHUB_REPOSITORY")); s != "" {
		return s, nil
	}
	root, err := Root()
	if err != nil {
		return "", err
	}
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	out, err := exec.Command("git", "-C", root, "remote", "get-url", "origin").Output()
	if err != nil {
		return "", fmt.Errorf("no GITHUB_REPOSITORY set and the origin remote could not be read: %w", err)
	}
	return slugFromRemote(string(out))
}

// slugFromRemote reads owner/name out of a GitHub remote URL, in either the
// https or the ssh form.
func slugFromRemote(remote string) (string, error) {
	u := strings.TrimSuffix(strings.TrimSpace(remote), ".git")
	i := strings.Index(u, "github.com")
	if i < 0 {
		return "", fmt.Errorf("the origin remote is not on GitHub, so it names no owner/name")
	}
	u = strings.TrimLeft(u[i+len("github.com"):], ":/")
	if strings.Count(u, "/") != 1 || strings.HasPrefix(u, "/") || strings.HasSuffix(u, "/") {
		return "", fmt.Errorf("could not read owner/name out of the origin remote")
	}
	return u, nil
}
