package main

import (
	"homelab/details/drafter"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every job of a workflow is reported, whatever its steps say, and the
// `on:` block's two-space keys, which look exactly like job keys, are not.
func TestWorkflowEgressReadsEveryJob(t *testing.T) {
	body := `name: "Example"
on:
  schedule:
    - cron: "0 * * * *"
  workflow_dispatch:

permissions:
  contents: read

jobs:
  checks:
    name: "Checks"
    steps:
      - name: Mobilize
        uses: $/.github/workflows/mobilize

  bare:
    name: "Bare"
    steps:
      - run: "true"
`

	docs, err := drafter.Documents([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	got := jobsIn("example.yml", docs)
	if len(got) != 2 {
		t.Fatalf("got %d jobs, want 2 (schedule and workflow_dispatch are not jobs): %+v", len(got), got)
	}
	keys := []string{got[0].Key(), got[1].Key()}
	if strings.Join(keys, " ") != "example.yml/bare example.yml/checks" {
		t.Errorf("jobs = %v, want both, by their keys", keys)
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

	got, err := declarations(body)
	if err != nil {
		t.Fatal(err)
	}
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

// A job the suppliers list does not name is refused, and one it names is
// silent. A new job must arrive declaring what it reaches, not unexamined.
func TestCompareRefusesAJobNobodyDeclared(t *testing.T) {
	declared := []declaredEgress{
		{Key: "a.yml/one", Policy: "block", Hosts: []string{"api.github.com:443"}},
		{Key: "a.yml/two", Policy: "audit", Reason: "vendor hosts are not knowable in advance"},
	}
	if got := Compare([]jobEgress{{Workflow: "a.yml", Job: "one"}, {Workflow: "a.yml", Job: "two"}}, declared); len(got) != 0 {
		t.Errorf("every job declared, and still refused: %v", got)
	}
	got := Compare([]jobEgress{{Workflow: "a.yml", Job: "one"}, {Workflow: "a.yml", Job: "two"}, {Workflow: "a.yml", Job: "three"}}, declared)
	if len(got) != 1 || !strings.Contains(got[0], "a.yml/three is not declared in the suppliers list") {
		t.Errorf("a job nobody declared was not refused by name: %v", got)
	}
}

// An entry for a job that no longer exists is a finding too. A list that keeps
// its dead entries is one nobody trusts, and the next reader cannot tell which
// half is real.
func TestCompareRefusesADeclarationWithNoJob(t *testing.T) {
	got := Compare(
		[]jobEgress{{Workflow: "a.yml", Job: "one"}},
		[]declaredEgress{
			{Key: "a.yml/one", Policy: "audit"},
			{Key: "a.yml/gone", Policy: "block", Hosts: []string{"api.github.com:443"}},
		})
	if len(got) != 1 || !strings.Contains(got[0], "which is not a job in any workflow") {
		t.Fatalf("want the stale entry reported, got %v", got)
	}
}

// The verb end to end, against a tree on disk: it passes a tree whose every
// job is declared, and refuses the same tree with a job added that is not.
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
      - name: Mobilize
        uses: $/.github/workflows/mobilize
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
		t.Fatalf("a tree whose every job is declared was refused (rc %d)", rc)
	}

	write(filepath.Join(workflowsDir, "example.yml"), workflow+`
  two:
    steps:
      - name: Mobilize
        uses: $/.github/workflows/mobilize
`)
	if rc := guardEgress([]string{"-root", dir}); rc == 0 {
		t.Fatal("a job the suppliers list does not name was let through")
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
