package parsed

import (
	"strings"
	"testing"
)

// SensitivePaths is where the list of paths that open a conversation on a
// pull request is.
const SensitivePaths = ".github/sensitive-paths"

// SensitiveEntry is one path the list declares sensitive, the reason it
// gives, and the line it is on.
type SensitiveEntry struct {
	Path string
	Why  string
	Line int
}

// SensitivePathsOf reads the list's plain-text format: one path per line,
// the text after "#" the reason, blank and whole-line comments ignored.
//
// Deliberately not a YAML parser. The file is read by a shell script in CI as
// well as here, and a format needing a dependency on both sides is a format
// that will eventually disagree with itself.
func SensitivePathsOf(body string) []SensitiveEntry {
	var out []SensitiveEntry
	for i, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		path, why, _ := strings.Cut(line, "#")
		out = append(out, SensitiveEntry{
			Path: strings.TrimSpace(path),
			Why:  strings.TrimSpace(why),
			Line: i + 1,
		})
	}
	return out
}

// SensitivePathList is the tree's list of sensitive paths, read.
func (r *Tree) SensitivePathList(t testing.TB) []SensitiveEntry {
	t.Helper()
	return SensitivePathsOf(string(r.read(t, SensitivePaths)))
}
