package main

import (
	"fmt"
	"regexp"
	"strings"
)

// Checking a finding before the operator has to.
//
// WHERE THIS CAME FROM. The clerk's first substantive finding was wrong (#233):
//
//	clerk / The work is unsound - Duplicate key 'ref' is specified inside the
//	upload-sarif step.
//
// There are three `ref:` keys in that file. One belongs to actions/checkout and
// two belong to the two upload-sarif steps, so each is in a different step's
// `with:` mapping and none duplicates another. The model saw three identical
// keys at the same indentation and reported a duplicate without tracking which
// mapping each belonged to. It read the file; it did not parse it.
//
// THE RULE THIS EXTENDS. `keep` already drops a finding that cannot be checked
// in ten seconds - one naming no file, or a file that was never read, or a line
// past the end of it. That rule is about whether a claim is CHECKABLE. This is
// the next step: where a claim is checkable MECHANICALLY, check it, and drop it
// if it is false. Every finding removed here is one the operator would
// otherwise have spent time disproving.
//
// WHY THIS CLASS AND NOT MORE. It is narrow by nature and that is the point. A
// claim about whether code is well structured is a judgement and stays one; a
// claim that a YAML file contains a duplicate key is a fact, settled by a
// parser in milliseconds. Only claims of the second kind belong here, and a
// check that is not exact does not belong at all - dropping a TRUE finding is a
// far worse failure than showing a false one, so anything this cannot settle
// with certainty it leaves alone.

// A claim that a structured file contains a duplicate key.
var duplicateKeyClaim = regexp.MustCompile(`(?i)\bduplicat(?:e|ed|ion)\b[^.]{0,80}?\bkey`)

// A quoted key name, so the claim can be checked against the key it names.
var quotedName = regexp.MustCompile(`['"` + "`" + `]([A-Za-z0-9_.-]+)['"` + "`" + `]`)

// falsified reports whether a finding makes a mechanically checkable claim that
// is false, and why.
//
// It returns false for everything it cannot settle. "I could not check this"
// and "this is false" must never be the same answer, because only one of them
// is a reason to throw a finding away.
func falsified(s snag, body string) (string, bool) {
	if body == "" {
		return "", false
	}
	if !duplicateKeyClaim.MatchString(s.Message) {
		return "", false
	}
	if !isYAML(s.Path) {
		return "", false
	}

	dupes := duplicateKeys(body)
	if len(dupes) == 0 {
		return fmt.Sprintf("claims a duplicate key in %s, and there is none", s.Path), true
	}

	// The file does contain a duplicate somewhere. If the finding named a key,
	// and that key is not one of them, the claim is still false.
	if m := quotedName.FindStringSubmatch(s.Message); m != nil {
		for _, d := range dupes {
			if d == m[1] {
				return "", false
			}
		}
		return fmt.Sprintf("claims %q is a duplicate key in %s; the duplicates there are %s",
			m[1], s.Path, strings.Join(dupes, ", ")), true
	}
	return "", false
}

func isYAML(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".yml") || strings.HasSuffix(lower, ".yaml")
}

// A mapping key at the start of a line, optionally opening a sequence entry.
var mappingKey = regexp.MustCompile(`^(\s*)(-\s+)?([A-Za-z0-9_.$-]+|"[^"]*"|'[^']*'):(\s|$)`)

// duplicateKeys returns the names that appear twice in one mapping.
//
// Tracked by indentation, which is what the model was not doing: two keys with
// the same name and the same indentation are duplicates only when they are in
// the same mapping, and a new sequence entry starts a new one. Three `ref:`
// keys in three `with:` blocks are three mappings, not one.
//
// Block scalars are skipped, because a line inside one is data. `key: value` in
// a heredoc-shaped payload is not a key at all, and counting it would make this
// check report duplicates that are not there - which would drop a true finding,
// the one outcome worth avoiding more than showing a false one.
func duplicateKeys(body string) []string {
	protected := yamlBlockScalars(body)

	type level struct {
		indent int
		keys   map[string]bool
	}
	var stack []level
	var found []string
	reported := map[string]bool{}

	for i, line := range strings.Split(body, "\n") {
		if protected[i] {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "---") {
			continue
		}

		m := mappingKey.FindStringSubmatch(line)
		if m == nil {
			// Not a key line. A bare sequence entry still closes any deeper
			// mapping, so the indentation stack stays honest.
			indent := indentOf(line)
			for len(stack) > 0 && stack[len(stack)-1].indent > indent {
				stack = stack[:len(stack)-1]
			}
			continue
		}

		indent := len(m[1])
		newItem := m[2] != ""
		if newItem {
			indent += len(m[2])
		}
		name := strings.Trim(m[3], `"'`)

		for len(stack) > 0 && stack[len(stack)-1].indent > indent {
			stack = stack[:len(stack)-1]
		}
		switch {
		case len(stack) == 0 || stack[len(stack)-1].indent < indent:
			stack = append(stack, level{indent: indent, keys: map[string]bool{}})
		case newItem:
			// A `- ` starts a fresh mapping at the same indentation, so the
			// keys of the previous entry are not in scope.
			stack[len(stack)-1].keys = map[string]bool{}
		}

		top := &stack[len(stack)-1]
		if top.keys[name] && !reported[name] {
			reported[name] = true
			found = append(found, name)
		}
		top.keys[name] = true
	}
	return found
}
