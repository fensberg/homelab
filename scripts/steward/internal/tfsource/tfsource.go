// Package tfsource reads a handful of named constants back out of this
// repository's OpenTofu source, so a Go test can assert that the Go
// implementation of a rule and the HCL implementation of the same rule still
// agree on the numbers.
//
// THIS IS NOT AN HCL PARSER, and must not grow into one. It does exactly one
// job: find `name = <literal>` at the top level of a .tf file and hand back
// the text on the right. Anything needing real evaluation - a local that
// references another local, a function call, a for-expression - is out of
// scope, because evaluating those correctly means running OpenTofu itself.
//
// Two alternatives were considered and rejected:
//
//   - hashicorp/hcl. It parses the syntax but does not evaluate `local.*`
//     references or functions, so it would not actually answer the question
//     any better than this does - and it would put an external dependency
//     into scripts/steward, which has none. That zero-dependency property is
//     load-bearing: it is why pr-validation.yml's Go steps need no go.sum
//     cache and why Trivy's gomod scan of the shipped binary has nothing to
//     find.
//   - `tofu console`. It is the real evaluator and would be exact, but it
//     needs `init`, downloaded providers and a resolvable config just to read
//     two integers, which turns an instant assertion into a slow one with
//     several new ways to fail for reasons unrelated to the thing under test.
//
// The brittleness here is deliberate. If someone restructures registry.tf so
// these lookups stop matching, the test fails with "not found in
// registry.tf" - which is the correct signal: the file that co-owns this
// contract changed shape, and whether the contract still holds is now a
// question a human has to answer.
package tfsource

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Read returns the contents of a .tf file, with comment lines stripped so a
// commented-out example cannot satisfy a lookup that the live code no longer
// does.
func Read(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	var out strings.Builder
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.String(), nil
}

// assignment finds `name = <rest of line>` and returns the trimmed right-hand
// side. Only single-line assignments - see the package comment.
func assignment(src, name string) (string, error) {
	re, err := regexp.Compile(`(?m)^\s*` + regexp.QuoteMeta(name) + `\s*=\s*(.+?)\s*$`)
	if err != nil {
		return "", err
	}
	m := re.FindAllStringSubmatch(src, -1)
	switch len(m) {
	case 0:
		return "", fmt.Errorf("no assignment to %q found", name)
	case 1:
		return m[0][1], nil
	default:
		// Two assignments to one name means the lookup is ambiguous and
		// whichever one this returned would be a coin flip.
		return "", fmt.Errorf("found %d assignments to %q; the lookup is ambiguous", len(m), name)
	}
}

// Int reads `name = <integer>`.
func Int(src, name string) (int, error) {
	raw, err := assignment(src, name)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s = %q is not an integer: %w", name, raw, err)
	}
	return n, nil
}

// String reads `name = "<text>"`.
func String(src, name string) (string, error) {
	raw, err := assignment(src, name)
	if err != nil {
		return "", err
	}
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return "", fmt.Errorf("%s = %s is not a quoted string literal", name, raw)
	}
	return raw[1 : len(raw)-1], nil
}

var mapEntry = regexp.MustCompile(`(?m)^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*"([^"]*)"\s*$`)

// Map reads `name = { k = "v" ... }`, a block of string-to-string entries.
// Entries that are not plain quoted strings are ignored rather than guessed
// at, so a block that gains a computed entry fails the caller's own
// comparison rather than silently returning a wrong value for it.
func Map(src, name string) (map[string]string, error) {
	start := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(name) + `\s*=\s*\{`)
	loc := start.FindStringIndex(src)
	if loc == nil {
		return nil, fmt.Errorf("no block assignment to %q found", name)
	}

	// Walk from the opening brace to its match, so a nested block does not
	// end the search early.
	depth := 0
	end := -1
	body := src[loc[1]-1:]
	for i, r := range body {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return nil, fmt.Errorf("block assignment to %q is never closed", name)
	}

	out := map[string]string{}
	for _, m := range mapEntry.FindAllStringSubmatch(body[1:end], -1) {
		out[m[1]] = m[2]
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("block assignment to %q holds no string entries", name)
	}
	return out, nil
}
