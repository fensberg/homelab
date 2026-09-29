// Package envfile reads a KEY=VALUE file such as scripts/versions.env.
//
// Written once: procurement and the security guard each carried the same
// parser (docs/epochs/02-abstraction.md, "A new custom block is refused").
package envfile

import "strings"

// Parse returns every KEY=VALUE line, trimmed. Blank lines and lines starting
// with # are skipped, and a line with no = is not an assignment.
func Parse(body string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}
