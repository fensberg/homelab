// Package main refuses to let a commit proceed while an undeclared third-party
// repository is installed as a commit hook.
//
// Default deny. Not "a test in CI notices afterwards" - by then the code has
// run on the machine holding the vault session, and the finding is archaeology.
// This runs first, before any hook does its work, and stops the commit.
//
// Two questions, and the second is the one nothing else could answer:
//
//	is every repo in .pre-commit-config.yaml declared as a supplier?
//	is every repo in the local cache declared as a supplier?
//
// The config can be read by anybody. The cache cannot: pre-commit clones into
// ~/.cache/pre-commit under generated names like `repornkulz89`, so an eighth
// repository sitting there looks exactly like the seven that belong. Answering
// that by hand needs somebody to notice a strange directory and know how
// pre-commit names things, which is not a control.
package main

import (
	"fmt"
	"sort"
	"strings"
)

// Finding is one repository that is installed or configured and not approved.
type Finding struct {
	Repo  string
	Where string // "the hook configuration" or "the local cache"
}

// Check compares what is configured and what is installed against what has been
// approved.
//
// Declared-but-unused is deliberately not a finding here. That is a tidiness
// problem for the test suite, and this runs on every commit: a guard that
// refuses a commit over an unused entry in a list is a guard somebody disables.
func Check(configured, cached, approved []string) []Finding {
	ok := map[string]bool{}
	for _, a := range approved {
		ok[normalise(a)] = true
	}

	seen := map[string]bool{}
	var out []Finding
	for _, r := range configured {
		if n := normalise(r); n != "" && !ok[n] && !seen["c"+n] {
			seen["c"+n] = true
			out = append(out, Finding{Repo: r, Where: "the hook configuration"})
		}
	}
	for _, r := range cached {
		if n := normalise(r); n != "" && !ok[n] && !seen["k"+n] {
			seen["k"+n] = true
			out = append(out, Finding{Repo: r, Where: "the local cache"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Repo < out[j].Repo })
	return out
}

// normalise makes two spellings of one repository compare equal. A remote can
// carry a .git suffix, a trailing slash, or an ssh scheme, and none of those
// make it a different supplier - while treating them as different would refuse
// a commit over punctuation and get the guard switched off.
func normalise(repo string) string {
	r := strings.TrimSpace(repo)
	r = strings.TrimSuffix(r, "/")
	r = strings.TrimSuffix(r, ".git")
	r = strings.TrimPrefix(r, "https://")
	r = strings.TrimPrefix(r, "http://")
	r = strings.TrimPrefix(r, "git@")
	r = strings.ReplaceAll(r, "github.com:", "github.com/")
	return strings.ToLower(r)
}

// Explain is what the operator reads when the commit is refused.
func Explain(findings []Finding) string {
	var b strings.Builder
	b.WriteString("refusing this commit: an unapproved supplier is installed as a commit hook.\n\n")
	for _, f := range findings {
		fmt.Fprintf(&b, "  %s\n      found in %s\n", f.Repo, f.Where)
	}
	b.WriteString(`
Every repository listed above runs on this machine, on every commit, with your
own privileges - not in a container and not in CI. That is a better position
than most software gets, and it is why nothing arrives here without somebody
deciding it should.

If it belongs, declare it under 'hooks:' in scripts/approved-suppliers.yml with
a reason somebody can read later. If it does not, remove the hook - and if it is
in the cache but not the configuration, it is left over from a hook that used to
be here, which 'pre-commit gc' removes.
`)
	return b.String()
}
