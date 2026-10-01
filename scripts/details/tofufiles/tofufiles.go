// Package tofufiles reads the repository's OpenTofu by what a file declares,
// never by where the file is.
//
// A check that opens modules/infrastructure/cluster/talos.tf checks that path. When the
// site's root was split in two, every check written that way went on reading
// the root it named: the one that listed the secrets OpenTofu creates found
// none in its directory and kept passing. Nothing had been removed; the thing
// it guarded had moved.
//
// So a check asks for "the file that declares this", or for every file, and
// gets an answer wherever the declaration lives - another root today, a module
// tomorrow. tests/go/repo refuses a test that names a root's files instead.
package tofufiles

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Read is every OpenTofu file the repository tracks under repoRoot, by
// repository-relative path, leaving out each root's and module's tests/
// directory: those hold fixtures rather than what is built.
//
// Tracked, rather than whatever is on disk: a provider cache, a scratch copy
// of a root and a dependency's own files are all on disk and none of them is
// this repository's. Finding none is an error, because a check handed nothing
// would pass having checked nothing.
func Read(repoRoot string) (map[string]string, error) {
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	out, err := exec.Command("git", "-C", repoRoot, "ls-files", "-z", "--", "*.tf").Output()
	if err != nil {
		return nil, fmt.Errorf("listing the OpenTofu files tracked under %s: %w", repoRoot, err)
	}
	files := map[string]string{}
	for _, rel := range strings.Split(string(out), "\x00") {
		if rel == "" || strings.Contains("/"+rel, "/tests/") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		if err != nil {
			return nil, err
		}
		files[rel] = string(body)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no tracked OpenTofu file was found under %s, so there is nothing to check", repoRoot)
	}
	return files, nil
}

// Code is a file's body with each line's comment removed, so a declaration
// described in a comment is not read as one.
func Code(body string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") {
			lines[i] = ""
		}
	}
	return strings.Join(lines, "\n")
}

// Declaring is the one file whose code contains declaration - a block header
// such as `resource "talos_machine_secrets" "this"`, or the start of an
// assignment such as `dns_resolvers =` - and that file's whole body.
//
// Runs of spaces are one space on both sides, so realigning a block's
// assignments does not hide a declaration from the check that reads it.
//
// Exactly one. None means the thing a check is about is gone, and the check
// must say so rather than pass; two means the question was ambiguous, and
// answering with either would check one and call both checked.
func Declaring(files map[string]string, declaration string) (path, body string, err error) {
	var found []string
	want := oneSpaced(declaration)
	for rel, b := range files {
		if strings.Contains(oneSpaced(Code(b)), want) {
			found = append(found, rel)
		}
	}
	sort.Strings(found)
	switch len(found) {
	case 1:
		return found[0], files[found[0]], nil
	case 0:
		return "", "", fmt.Errorf("no OpenTofu file declares %q. If it was renamed or removed, the check that reads it has nothing to read", declaration)
	}
	return "", "", fmt.Errorf("%q is declared in %d files (%s), so there is no one file to read. Ask for something only one of them declares, or read them all", declaration, len(found), strings.Join(found, ", "))
}

// oneSpaced collapses each run of spaces and tabs to one space.
func oneSpaced(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.Join(strings.Fields(line), " ")
	}
	return strings.Join(lines, "\n")
}

// In is the files of one directory, for a check that is about a root or a
// module as a whole and has found it by what it declares.
func In(files map[string]string, dir string) map[string]string {
	out := map[string]string{}
	for rel, b := range files {
		if filepath.ToSlash(filepath.Dir(rel)) == dir {
			out[rel] = b
		}
	}
	return out
}
