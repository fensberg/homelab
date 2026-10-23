// Package deliveries reads scripts/deliveries.lock: one section per delivery,
// each opened by a header naming its kind, name and version, and closed by
// # [end].
//
// Written once: the security guard that refuses a lock disagreeing with what
// it was made from read it with a parser of its own, and the repository's
// guards are the second reader.
package deliveries

import (
	"fmt"
	"regexp"
	"strings"
)

// Path is the lock, relative to the repository's top.
const Path = "scripts/deliveries.lock"

// Section is one delivery's section of the lock: its kind (pypi, fetch), its
// name, the versions.env key and version it was made for, and its lines -
// the pinned requirements, or the URL and its hash.
type Section struct {
	Kind       string
	Name       string
	VersionKey string
	Version    string
	Lines      []string
}

var header = regexp.MustCompile(`^# \[([a-z]+): (\S+) ([A-Z0-9_]+)=(\S+)\]$`)

// Parse reads the lock's sections, keyed "kind:name". A section opened
// inside another, closed twice, never closed or written twice is refused.
func Parse(body string) (map[string]Section, error) {
	out := map[string]Section{}
	var cur *Section
	for n, line := range strings.Split(body, "\n") {
		switch {
		case header.MatchString(line):
			if cur != nil {
				return nil, fmt.Errorf("line %d opens %q before %s is closed with # [end]", n+1, line, cur.Name)
			}
			m := header.FindStringSubmatch(line)
			cur = &Section{Kind: m[1], Name: m[2], VersionKey: m[3], Version: m[4]}
		case line == "# [end]":
			if cur == nil {
				return nil, fmt.Errorf("line %d closes a section that was never opened", n+1)
			}
			key := cur.Kind + ":" + cur.Name
			if _, dup := out[key]; dup {
				return nil, fmt.Errorf("%s has two sections", key)
			}
			out[key] = *cur
			cur = nil
		case cur != nil && strings.TrimSpace(line) != "" && !strings.HasPrefix(line, "#"):
			cur.Lines = append(cur.Lines, line)
		}
	}
	if cur != nil {
		return nil, fmt.Errorf("the section for %s is never closed with # [end]", cur.Name)
	}
	return out, nil
}
