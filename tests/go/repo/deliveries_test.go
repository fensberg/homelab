package repo

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Guards over how tools arrive and who orders them (#416).
//
// scripts/deliveries.lock pins every tool this estate installs that has no
// lockfile of its own ecosystem, by version and by hash, and
// scripts/take-delivery.sh is the only thing that installs from it. Two ways
// that stops being true without anything else noticing: a second installer
// appears beside it, or the lock is written by something holding a credential
// it has no business holding. This refuses the first; the second is a
// configuration rule (policy/conftest/expedite_duty.rego). Whether the lock
// agrees with its declarations is `security guard-deliveries`, which runs on
// every commit.

// takeDelivery is the one file allowed to install from PyPI.
const takeDelivery = "scripts/take-delivery.sh"

// An install from a Python package index, in any of the spellings in use.
var pythonInstall = regexp.MustCompile(`\b(?:pip3?|pipx)\s+install\b|\buv\s+(?:pip|tool)\s+install\b`)

// A whole-line comment in any of the languages this repository writes. A line
// that only mentions an install is prose, and the history of one - "this used
// to be pipx install pre-commit" - is exactly what a comment here should say.
var commentLine = regexp.MustCompile(`^\s*(?:#|//|\*|<!--|--)`)

func TestNothingInstallsFromPyPIOutsideTakeDelivery(t *testing.T) {
	root := repoRoot(t)

	var offenders []string
	scanned := 0
	for _, rel := range trackedFilesIn(t, root) {
		if !authoredHere(rel) || rel == takeDelivery {
			continue
		}
		// Markdown is read, not run: an epoch record quoting the install that
		// used to happen is history, and nothing in the estate executes it.
		if strings.HasSuffix(rel, ".md") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		scanned++
		for n, line := range strings.Split(string(body), "\n") {
			if commentLine.MatchString(line) || !pythonInstall.MatchString(line) {
				continue
			}
			// This file carries the shapes in order to refuse them, and the
			// mutation ledger carries one in order to plant it and watch this
			// refuse it. Neither is run.
			if strings.HasSuffix(rel, "/deliveries_test.go") || isLedger(rel) {
				continue
			}
			offenders = append(offenders, rel+":"+strconv.Itoa(n+1)+": "+strings.TrimSpace(line))
		}
	}
	if scanned < 100 {
		t.Fatalf("only %d files were scanned, which is not this repository - the enumeration has stopped matching", scanned)
	}
	if len(offenders) > 0 {
		t.Errorf(`something installs from a Python package index outside %s:

  %s

A version pin written anywhere else fixes the tool and lets every dependency it
pulls in resolve fresh on each install - over a hundred packages for checkov
alone. Declare the tool with pypi: on its tools: entry in
scripts/approved-suppliers.yml, pin its version in scripts/versions.env, run
task order-deliveries, and install it with:

    %s <name>`, takeDelivery, strings.Join(offenders, "\n  "), takeDelivery)
	}
}
