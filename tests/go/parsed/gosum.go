package parsed

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// GoSumLine is one line of a go.sum: a module, a version, and the hash Go
// recorded for it.
type GoSumLine struct {
	Module, Version, Hash string
}

var goSumLine = regexp.MustCompile(`^(\S+) (v\S+) (h1:[A-Za-z0-9+/]+=*)$`)

// GoSum is a go.sum of the tree, line by line, and an error naming the first
// line Go would not have written.
func (r *Tree) GoSum(t testing.TB, rel string) ([]GoSumLine, error) {
	t.Helper()
	return GoSumOf(string(r.read(t, rel)))
}

// GoSumOf parses the lines of a go.sum.
func GoSumOf(body string) ([]GoSumLine, error) {
	var out []GoSumLine
	for i, line := range strings.Split(strings.TrimSpace(body), "\n") {
		m := goSumLine.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("line %d is not one Go writes: %q", i+1, line)
		}
		out = append(out, GoSumLine{m[1], m[2], m[3]})
	}
	return out, nil
}
