package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The workflow parser has to see what the file says, including the cases that
// are not a tidy allowlist: a job with no harden-runner at all, and the `on:`
// block's two-space keys, which look exactly like job keys and are not.
func TestWorkflowEgressReadsEveryJob(t *testing.T) {
	body := `name: "Example"
on:
  schedule:
    - cron: "0 * * * *"
  workflow_dispatch:

permissions:
  contents: read

jobs:
  hardened:
    name: "Hardened"
    steps:
      - name: Harden Runner
        uses: step-security/harden-runner@e14015d # v2.21.1
        with:
          egress-policy: block
          allowed-endpoints: >
            api.github.com:443
            github.com:443

      - name: Checkout Code
        uses: actions/checkout@3d3c42e # v7.0.1

  audited:
    name: "Audited"
    steps:
      - name: Harden Runner
        uses: step-security/harden-runner@e14015d # v2.21.1
        with:
          egress-policy: audit

  unguarded:
    name: "Unguarded"
    steps:
      - name: Checkout Code
        uses: actions/checkout@3d3c42e # v7.0.1
`

	got := workflowEgress("example.yml", body)
	if len(got) != 3 {
		t.Fatalf("got %d jobs, want 3 (schedule and workflow_dispatch are not jobs): %+v", len(got), got)
	}

	if got[0].Key() != "example.yml/hardened" || got[0].Policy != "block" {
		t.Errorf("first job = %+v", got[0])
	}
	if strings.Join(got[0].Hosts, " ") != "api.github.com:443 github.com:443" {
		t.Errorf("hosts = %v, want both endpoints and nothing from the following step", got[0].Hosts)
	}
	if got[1].Policy != "audit" || len(got[1].Hosts) != 0 {
		t.Errorf("audited job = %+v, want audit with no hosts", got[1])
	}
	// The one that matters: a job nothing hardens must be reported as a job,
	// not skipped. Skipping it is how a new job arrives unexamined.
	if got[2].Key() != "example.yml/unguarded" || got[2].Policy != "" {
		t.Errorf("unguarded job = %+v, want it present with no policy", got[2])
	}
}

// The declarations parser reads one section and stops at the next.
func TestDeclarationsReadOnlyTheEgressSection(t *testing.T) {
	body := `endpoints:
  - group: source-control
    hosts:
      - api.github.com:443

egress:
  - job: example.yml/hardened
    policy: block
    hosts:
      - api.github.com:443
      - github.com:443

  - job: example.yml/audited
    policy: audit
    reason: >-
      Reaches a vendor whose hosts are not knowable in advance.

exemptions:
  - job: example.yml/not-a-declaration
    policy: block
`

	got := declarations(body)
	if len(got) != 2 {
		t.Fatalf("got %d declarations, want 2 - the exemptions section is not egress: %+v", len(got), got)
	}
	if got[0].Key != "example.yml/hardened" || got[0].Policy != "block" || len(got[0].Hosts) != 2 {
		t.Errorf("first declaration = %+v", got[0])
	}
	if got[1].Policy != "audit" || len(got[1].Hosts) != 0 {
		t.Errorf("second declaration = %+v, want audit with no hosts", got[1])
	}
}

// Every way the two can disagree, each proved by its own counterexample. The
// table is the guard: a rule with no row here is a rule nothing would notice
// losing.
func TestCompareRefusesEveryKindOfDisagreement(t *testing.T) {
	declared := []declaredEgress{
		{Key: "a.yml/one", Policy: "block", Hosts: []string{"api.github.com:443", "github.com:443"}},
		{Key: "a.yml/two", Policy: "audit", Reason: "vendor hosts are not knowable in advance"},
	}

	cases := []struct {
		name  string
		found []jobEgress
		wants string // a phrase the finding must carry, or "" for no finding
	}{
		{
			name: "agreement is silence",
			found: []jobEgress{
				{Workflow: "a.yml", Job: "one", Policy: "block", Hosts: []string{"github.com:443", "api.github.com:443"}},
				{Workflow: "a.yml", Job: "two", Policy: "audit"},
			},
		},
		{
			name:  "a job nobody declared",
			found: []jobEgress{{Workflow: "a.yml", Job: "three", Policy: "block", Hosts: []string{"api.github.com:443"}}},
			wants: "is not declared in the suppliers list",
		},
		{
			name:  "a job nothing hardens",
			found: []jobEgress{{Workflow: "a.yml", Job: "three"}},
			wants: "hardens nothing",
		},
		{
			name:  "a policy that is not the declared one",
			found: []jobEgress{{Workflow: "a.yml", Job: "one", Policy: "audit"}},
			wants: "sets egress-policy: audit and the suppliers list declares block",
		},
		{
			name: "a host the declaration does not carry",
			found: []jobEgress{{Workflow: "a.yml", Job: "one",
				Policy: "block", Hosts: []string{"api.github.com:443", "github.com:443", "evil.example.com:443"}}},
			wants: "allows evil.example.com:443, which the suppliers list does not declare",
		},
		{
			name: "a declared host the workflow dropped",
			found: []jobEgress{{Workflow: "a.yml", Job: "one",
				Policy: "block", Hosts: []string{"api.github.com:443"}}},
			wants: "is declared to reach github.com:443 and its workflow does not allow it",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Every case declares both jobs, so a missing one is only ever the
			// disagreement under test rather than a stale-entry finding too.
			found := c.found
			if c.name != "agreement is silence" {
				found = append(found, jobEgress{Workflow: "a.yml", Job: "two", Policy: "audit"})
				if c.found[0].Job != "one" {
					found = append(found, jobEgress{Workflow: "a.yml", Job: "one",
						Policy: "block", Hosts: []string{"api.github.com:443", "github.com:443"}})
				}
			}
			got := Compare(found, declared)
			if c.wants == "" {
				if len(got) != 0 {
					t.Fatalf("want no findings, got %v", got)
				}
				return
			}
			if len(got) == 0 {
				t.Fatalf("want a finding mentioning %q, got none", c.wants)
			}
			if !strings.Contains(strings.Join(got, "\n"), c.wants) {
				t.Errorf("findings do not mention %q:\n%s", c.wants, strings.Join(got, "\n"))
			}
		})
	}
}

// An entry for a job that no longer exists is a finding too. A list that keeps
// its dead entries is one nobody trusts, and the next reader cannot tell which
// half is real.
func TestCompareRefusesADeclarationWithNoJob(t *testing.T) {
	got := Compare(
		[]jobEgress{{Workflow: "a.yml", Job: "one", Policy: "audit"}},
		[]declaredEgress{
			{Key: "a.yml/one", Policy: "audit"},
			{Key: "a.yml/gone", Policy: "block", Hosts: []string{"api.github.com:443"}},
		})
	if len(got) != 1 || !strings.Contains(got[0], "which is not a job in any workflow") {
		t.Fatalf("want the stale entry reported, got %v", got)
	}
}

// The verb end to end, against a tree on disk.
//
// Both halves are checked: that it passes on a tree that agrees, and that the
// same tree with one endpoint added fails. A guard proved only in the passing
// direction is a guard that would pass if it did nothing at all.
func TestTheVerbJudgesATreeOnDisk(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, workflowsDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}

	workflow := `name: "Example"
jobs:
  one:
    steps:
      - name: Harden Runner
        uses: step-security/harden-runner@e14015d # v2.21.1
        with:
          egress-policy: block
          allowed-endpoints: >
            api.github.com:443
`
	suppliers := `egress:
  - job: example.yml/one
    policy: block
    hosts:
      - api.github.com:443
`
	write := func(rel, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(workflowsDir, "example.yml"), workflow)
	write(suppliersPath, suppliers)

	if rc := guardEgress([]string{"-root", dir}); rc != 0 {
		t.Fatalf("a tree that agrees was refused (rc %d)", rc)
	}

	write(filepath.Join(workflowsDir, "example.yml"),
		strings.Replace(workflow, "            api.github.com:443\n",
			"            api.github.com:443\n            evil.example.com:443\n", 1))
	if rc := guardEgress([]string{"-root", dir}); rc == 0 {
		t.Fatal("a workflow reaching an endpoint the suppliers list does not declare was allowed")
	}

	// And an undeclared job, which is the fail-closed case: a new job must
	// arrive with no egress rather than with an allowlist nobody examined.
	write(filepath.Join(workflowsDir, "example.yml"), workflow+`
  two:
    steps:
      - name: Harden Runner
        uses: step-security/harden-runner@e14015d # v2.21.1
        with:
          egress-policy: audit
`)
	if rc := guardEgress([]string{"-root", dir}); rc == 0 {
		t.Fatal("a job the suppliers list does not name was allowed to call out")
	}
}

// An empty declaration section must refuse rather than pass everything.
//
// Removing the section is the cheapest way to make this guard green, so it has
// to be the loudest way to make it red.
func TestTheVerbRefusesASuppliersListWithNoEgressSection(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, workflowsDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, suppliersPath), []byte("registries:\n  - host: ghcr.io\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rc := guardEgress([]string{"-root", dir}); rc == 0 {
		t.Fatal("a suppliers list declaring no egress at all was accepted")
	}
}
