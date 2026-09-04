package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// A snag is one defect, in one place.
//
// A snagging list is what a clerk of works produces after walking the work:
// discrete items, each pinned to where it is, each either "this is not built
// soundly" or "this does not match the drawings". Prose in one comment is the
// wrong shape for that - it cannot be dismissed item by item, cannot close
// itself when the defect goes, and cannot be counted.

const (
	// A stranger who cloned this repository cannot get past something.
	ruleHandover = "handover-gap"

	// The work itself is unsound: something unreachable, a part that connects
	// to nothing else, a call that goes nowhere, a variable written and read
	// by nobody but itself.
	ruleUnsound = "unsound-work"

	// The commentary claims something the code does not do. Found by asking a
	// reader that was never shown the commentary what the code does, and only
	// then showing it what was claimed.
	ruleDisagrees = "commentary-disagrees"
)

type snag struct {
	Rule    string `json:"rule"`
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Message string `json:"message"`
}

// keep drops every finding that cannot be checked in ten seconds.
//
// This is the citation rule, enforced rather than requested. A finding that
// names no file, or names a file that was never read, or points past the end
// of it, is not a finding - it is something the model produced. Asking for
// citations in a prompt gets them most of the time; requiring them here gets
// them always, and the operator's time is the thing being protected.
//
// Silently dropping would be its own blind spot, so the count of what was
// dropped is returned and reported.
func keep(found []snag, lines map[string]int) (kept []snag, dropped []string) {
	seen := map[string]bool{}

	for _, s := range found {
		switch {
		case s.Rule != ruleUnsound && s.Rule != ruleDisagrees && s.Rule != ruleHandover:
			dropped = append(dropped, fmt.Sprintf("unknown rule %q", s.Rule))
		case strings.TrimSpace(s.Message) == "":
			dropped = append(dropped, "no message")
		case lines[s.Path] == 0:
			dropped = append(dropped, fmt.Sprintf("names %q, which was not read", s.Path))
		case s.Line < 1 || s.Line > lines[s.Path]:
			dropped = append(dropped, fmt.Sprintf("%s:%d is outside the file (%d lines)", s.Path, s.Line, lines[s.Path]))
		default:
			key := fmt.Sprintf("%s|%s|%s", s.Rule, s.Path, s.Message)
			if seen[key] {
				dropped = append(dropped, fmt.Sprintf("%s:%d repeats an earlier finding", s.Path, s.Line))
				continue
			}
			seen[key] = true
			kept = append(kept, s)
		}
	}
	return kept, dropped
}

// parse reads what the model returned.
//
// Models wrap JSON in prose and in code fences however firmly they are asked
// not to, so the fence is stripped rather than made a condition of the answer
// being usable. A response that still is not JSON is an error and never an
// empty finding list: no findings and could-not-read must not look the same.
func parse(answer string) ([]snag, error) {
	text := unfence(answer)

	var found []snag
	if err := json.Unmarshal([]byte(text), &found); err != nil {
		return nil, fmt.Errorf("the answer was not a JSON list of findings: %w", err)
	}
	return found, nil
}

// unfence takes the JSON out of a code fence.
//
// Models fence their JSON however firmly they are asked not to, so this is
// tolerated rather than made a condition of the answer being usable.
func unfence(answer string) string {
	text := strings.TrimSpace(answer)
	if i := strings.Index(text, "```"); i >= 0 {
		text = text[i+3:]
		if nl := strings.IndexByte(text, '\n'); nl >= 0 {
			text = text[nl+1:]
		}
		if j := strings.Index(text, "```"); j >= 0 {
			text = text[:j]
		}
	}
	return strings.TrimSpace(text)
}

// fingerprint is how an alert survives the file moving underneath it.
//
// GitHub matches alerts between runs on this, which is what lets one close
// itself when the defect goes and reopen if it returns. The line is
// deliberately not in it: a finding that shifts down four lines because
// something was inserted above is the same finding, and a new alert every time
// anybody edits the file is how a surface becomes noise.
func (s snag) fingerprint() string {
	sum := sha256.Sum256([]byte(s.Rule + "\x00" + s.Path + "\x00" + s.Message))
	return hex.EncodeToString(sum[:])[:32]
}

// parseBlind reads the first pass, which returns an account as well as findings.
//
// The account is not shown to anybody. It exists so the second pass can be
// asked about the commentary without ever seeing the code, which is what stops
// it reading the claim and then finding the claim.
func parseBlind(answer string) (account string, found []snag, err error) {
	text := unfence(answer)
	var wrapper struct {
		Account  string `json:"account"`
		Findings []snag `json:"findings"`
	}
	if err := json.Unmarshal([]byte(text), &wrapper); err != nil {
		return "", nil, fmt.Errorf("the first pass did not answer with an account and findings: %w", err)
	}
	if strings.TrimSpace(wrapper.Account) == "" {
		return "", nil, fmt.Errorf("the first pass returned no account, so there is nothing to compare the commentary against")
	}
	return wrapper.Account, wrapper.Findings, nil
}
