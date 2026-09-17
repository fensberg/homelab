package repo

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
// it has no business holding. These refuse both. Whether the lock agrees with
// its declarations is `security guard-deliveries`, which runs on every commit.

// takeDelivery is the one file allowed to install from PyPI.
const takeDelivery = "scripts/take-delivery.sh"

// An install from a Python package index, in any of the spellings in use.
var pythonInstall = regexp.MustCompile(`\b(?:pip3?|pipx)\s+install\b|\buv\s+(?:pip|tool)\s+install\b`)

// A whole-line comment in any of the languages this repository writes. A line
// that only mentions an install is prose, and the history of one - "this used
// to be pipx install pre-commit" - is exactly what a comment here should say.
var commentLine = regexp.MustCompile(`^\s*(?:#|//|\*|<!--|--)`)

func TestNothingInstallsFromPyPIOutsideTakeDelivery(t *testing.T) {
	root := intendedRoot(t)

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
			if strings.HasSuffix(rel, "/deliveries_test.go") || rel == "tests/mutations.yml" {
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

// The bypass belongs to the expediting duty, not to procurement.
//
// Procurement is one program with several duties. Expediting is the only one
// that holds elevated permission - its App may merge its own delivery without
// a review, bounded by `superintendent enforce-standing-order`. Ordering writes
// scripts/deliveries.lock and holds nothing; its output is committed through an
// ordinary review.
//
// So the credential must never run a verb outside its duty. A job that mints
// it and then runs `procurement order` would write the lock with a token that
// can merge without review - a supply-chain decision taken under a standing
// order that covers one digest. And ordering needs no workflow at all: it is
// run by a person, and read by security on every commit.
//
// The workflows holding the credential are FOUND, by the secret they read,
// rather than named here, so renaming the workflow or adding a second one
// cannot step outside this.
var (
	procurementVerb = regexp.MustCompile(`(?:go run -C scripts/procurement \.|toolshed/procurement)\s+([a-z][a-z0-9-]*)`)
	expediteSecret  = regexp.MustCompile(`secrets\.EXPEDITE_BOT_[A-Z_]+`)
)

func TestOnlyTheExpediteDutyRunsUnderItsCredential(t *testing.T) {
	workflows := intendedWorkflows(t)
	if len(workflows) == 0 {
		t.Fatal("no workflows were read, so nothing was checked")
	}

	names := make([]string, 0, len(workflows))
	for name := range workflows {
		names = append(names, name)
	}
	sort.Strings(names)

	holders := 0
	expediteVerbs := 0
	for _, name := range names {
		body := workflows[name]
		holds := expediteSecret.MatchString(body)
		if holds {
			holders++
		}
		for _, m := range procurementVerb.FindAllStringSubmatch(body, -1) {
			verb := m[1]
			switch {
			case verb == "order":
				t.Errorf("%s runs `procurement order`.\n\n"+
					"Ordering writes scripts/deliveries.lock, which decides what every lane and "+
					"workstation installs. It is run by a person and committed through review; "+
					"no workflow orders, and above all not one that could hold the expedite "+
					"credential.", name)
			case !strings.HasPrefix(verb, "expedite-"):
				if holds {
					t.Errorf("%s holds the expedite credential and runs `procurement %s`, "+
						"which is not an expedite-* verb.\n\n"+
						"The bypass belongs to the expediting duty, not to procurement. "+
						"Run it somewhere that holds no such credential.", name, verb)
				}
			default:
				if !holds {
					t.Errorf("%s runs `procurement %s` and holds no expedite credential; "+
						"expediting runs in the workflow that does, so the duty and its "+
						"bounds stay in one place", name, verb)
				}
				expediteVerbs++
			}
		}
	}
	// Floors, so a rename that stops either pattern matching fails here rather
	// than passing over workflows it can no longer read.
	if holders == 0 {
		t.Error("no workflow reads an EXPEDITE_BOT_ secret, so this cannot tell which one holds the " +
			"credential - the secret was renamed, and this guard is asserting nothing")
	}
	if expediteVerbs == 0 {
		t.Error("no workflow runs a procurement expedite-* verb, so the pattern that finds " +
			"procurement's verbs has stopped matching and nothing was checked")
	}
}
