package tofustate

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Move is one `moved` block: a resource renamed in the source, which state
// still knows by its old address until the rename is recorded.
type Move struct{ From, To string }

// movedBlock matches one `moved { ... }`. These blocks never nest, so stopping
// at the first closing brace is correct rather than lucky.
var movedBlock = regexp.MustCompile(`(?s)\bmoved\s*\{(.*?)\}`)

// movedEndpoint pulls the address off a `from =` or `to =` line. Addresses are
// bare references, never quoted, so this deliberately does not accept a
// string.
var movedEndpoint = regexp.MustCompile(`(?m)^\s*(from|to)\s*=\s*([A-Za-z_][\w.\[\]"-]*)\s*$`)

// Moves reads every `moved` block in the OpenTofu source at dir, in file and
// source order.
//
// It reads the source rather than asking OpenTofu, because the question is
// "what does the configuration say", and a plan that would answer it is the
// plan a pending move refuses to build. Not an HCL parser, and it must not
// grow into one: one small reader with one job.
//
// A block without both a from and a to is an error rather than skipped. A move
// this cannot read is a move nothing settles, and every targeted apply after
// it is refused.
func Moves(dir string) ([]Move, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".tf") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	var moves []Move
	for _, name := range names {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", name, err)
		}
		for _, block := range movedBlock.FindAllStringSubmatch(string(body), -1) {
			var m Move
			for _, ep := range movedEndpoint.FindAllStringSubmatch(block[1], -1) {
				if ep[1] == "from" {
					m.From = ep[2]
				} else {
					m.To = ep[2]
				}
			}
			if m.From == "" || m.To == "" {
				return nil, fmt.Errorf("%s has a moved block without both a from and a to, so nothing can settle it", name)
			}
			moves = append(moves, m)
		}
	}
	return moves, nil
}
