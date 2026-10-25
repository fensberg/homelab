package repo

import (
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"homelab/tests/parsed"
)

// The configuration rules: policy/conftest, run by Conftest over every file
// of a kind tests/languages.yml says it parses.
//
// WHY THEY ARE NOT GO. The rules were Go tests that read a workflow or a
// manifest and matched text in it. That is a policy engine built by hand,
// and every such guard could be satisfied by the comment explaining the thing
// rather than by the thing. Conftest parses each file the way its own tool
// does and the rules are written over the parsed structure, so a comment is
// not there to be matched; it is the industry's tool for this, and each rule
// carries tests of its own (decided 2026-10-16).
//
// WHY THIS RUNS THEM. The mutation ledger proves every guard fails when what
// it guards is broken, on a copy of the real tree. Running Conftest from here
// keeps that proof for every rule moved: a ledger entry names this test and
// the rule that must refuse the mutation.

// conftestProgram is the policy engine, delivered by scripts/take-delivery.sh.
const conftestProgram = "conftest"

// conftestRules is where the configuration rules live.
const conftestRules = "policy/conftest"

// conftestInputs is every tracked file of a kind the declaration hands to
// Conftest, except the code rules and their examples.
func conftestInputs(t *testing.T) []string {
	t.Helper()
	kinds := map[string]bool{}
	for _, g := range loadLanguages(t).Governed {
		if slices.Contains(g.ParsedBy, conftestProgram) {
			kinds[g.Kind] = true
		}
	}
	if len(kinds) == 0 {
		t.Fatal("tests/languages.yml hands no kind of file to conftest, so the configuration rules would read nothing")
	}
	var files []string
	for _, rel := range trackedFiles(t) {
		if kinds[kindOf(rel)] && !isCodeRuleFile(rel) {
			files = append(files, rel)
		}
	}
	sort.Strings(files)
	if len(files) < 100 {
		t.Fatalf("only %d files are handed to conftest, which is not this repository - the enumeration has stopped matching", len(files))
	}
	return files
}

// runConftest runs the policy engine in the repository with the given
// arguments, and gives what it said and whether it passed.
func runConftest(t *testing.T, args ...string) (string, bool) {
	t.Helper()
	if _, err := exec.LookPath(conftestProgram); err != nil {
		t.Fatal("conftest is not on PATH; take delivery of it: scripts/take-delivery.sh conftest")
	}
	stdout, stderr, err := repository(t).Invoke(parsed.Run{Env: []string{"NO_COLOR=1"}}, conftestProgram, args...)
	out := string(stdout) + string(stderr)
	if err != nil {
		if _, failed := err.(*exec.ExitError); !failed {
			t.Fatalf("conftest could not be run: %v", err)
		}
		return out, false
	}
	return out, true
}

func TestTheConfigurationRulesHold(t *testing.T) {
	if !repository(t).IsDir(conftestRules) {
		t.Fatalf("%s is not there, so no configuration rule runs", conftestRules)
	}
	heavy(t, "plans every site's roots from nothing, for the rules over OpenTofu, which takes seconds")
	// How it is run - every package, every file at once - is conftest.toml's
	// to say, so this, the lane and a person at a terminal run it alike. The
	// plans go with the files: OpenTofu is judged on what it would make, as
	// it works that out, and not on its source.
	args := append(append([]string{"test"}, conftestInputs(t)...), repository(t).UnbuiltPlans(t)...)
	out, ok := runConftest(t, args...)
	if !ok {
		var refused []string
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "FAIL") {
				refused = append(refused, strings.TrimSpace(line))
			}
		}
		if len(refused) == 0 {
			t.Fatalf("conftest did not run the rules:\n\n%s", out)
		}
		t.Errorf("a configuration rule refuses the repository:\n\n  %s\n\nThe rule is in %s, named by its package; its own tests beside it show what it accepts.",
			strings.Join(refused, "\n  "), conftestRules)
	}
}

// Every rule is held to what it claims by tests of its own: Conftest's
// verify runs each *_test.rego beside the rules.
func TestEveryConfigurationRuleProvesItself(t *testing.T) {
	collectedPackages(t)
	out, ok := runConftest(t, "verify")
	if !ok {
		t.Errorf("a configuration rule's own tests fail:\n\n%s", out)
	}
	if !strings.Contains(out, "passed") {
		t.Errorf("conftest verify ran no tests, so no rule has shown it refuses anything:\n\n%s", out)
	}
}

// regalProgram is OPA's own linter for Rego, delivered by
// scripts/take-delivery.sh.
const regalProgram = "regal"

// Every rule is written as OPA's linter would have it: one property to a
// package, each in a directory of its own, and its refusals together. What
// Regal checks and how strictly is .regal/config.yaml's to say, so this, the
// lane and a person at a terminal lint alike.
func TestTheConfigurationRulesAreWrittenAsTheirLinterWouldHaveThem(t *testing.T) {
	heavy(t, "lints every policy file with regal")
	if _, err := exec.LookPath(regalProgram); err != nil {
		t.Fatal("regal is not on PATH; take delivery of it: scripts/take-delivery.sh regal")
	}
	stdout, stderr, err := repository(t).Invoke(parsed.Run{Env: []string{"NO_COLOR=1"}}, regalProgram, "lint", conftestRules)
	if err != nil {
		if _, failed := err.(*exec.ExitError); !failed {
			t.Fatalf("regal could not be run: %v", err)
		}
		t.Errorf("a policy file is not written as Regal would have it:\n\n%s%s", stdout, stderr)
	}
}

// opaProgram is the policy engine itself, delivered by scripts/take-delivery.sh:
// Conftest runs the rules on it, and this reads them with it.
const opaProgram = "opa"

// regoTree is a policy file as OPA parses it: its package's path and the
// whole syntax tree, to be walked.
type regoTree struct {
	Package string
	Tree    any
}

// parseRego is a policy file as OPA parses it, with its package read off.
func parseRego(t *testing.T, rel string) regoTree {
	t.Helper()
	whole := repository(t).Rego(t, rel)
	return regoTree{regoPackage(whole), whole}
}

// regoPackage is the package a policy file's syntax tree declares, past data.
func regoPackage(tree any) string {
	root, _ := tree.(map[string]any)
	pkg, _ := root["package"].(map[string]any)
	path, _ := pkg["path"].([]any)
	var parts []string
	for i, p := range path {
		if i == 0 {
			continue // data
		}
		parts = append(parts, termValue(p))
	}
	return strings.Join(parts, ".")
}

// readsInput is how many times a syntax tree names the input.
func readsInput(node any) int {
	n := 0
	switch v := node.(type) {
	case map[string]any:
		if v["type"] == "var" && v["value"] == "input" {
			n++
		}
		for _, child := range v {
			n += readsInput(child)
		}
	case []any:
		for _, child := range v {
			n += readsInput(child)
		}
	}
	return n
}

// policyFiles is every policy file beside the rules, parsed: the rules, lib,
// main and the tests, each known by the package it declares.
func policyFiles(t *testing.T) map[string]regoTree {
	t.Helper()
	files := map[string]regoTree{}
	for _, rel := range trackedFiles(t) {
		if strings.HasPrefix(rel, conftestRules+"/") && strings.HasSuffix(rel, ".rego") {
			files[rel] = parseRego(t, rel)
		}
	}
	if len(files) < 10 {
		t.Fatalf("only %d policy files were read from %s, which is not this repository", len(files), conftestRules)
	}
	return files
}

// isTestPackage reports whether a package is a rule's tests. Known by what
// it declares, never by its file's name: Conftest runs a rule written in a
// file named like a test, so a name was a place to hide one.
func isTestPackage(pkg string) bool { return strings.HasSuffix(pkg, "_test") }

// A rule reads its files through the selectors in lib and names the input
// nowhere else. A rule that picks its own files by testing paths is a place
// it reads that nothing names, and no guard can prove a place it cannot see:
// the code rules' scope was proved place by place (#779), and these rules
// had 161 such tests across 46 packages. Starting from nothing, the input is
// lib's alone, and lib is one file. Read through OPA's own parser, so a
// comment or a string that says "input" is neither (agreed 2026-10-18).
//
// Two ways round it were found and are closed here: a second file declaring
// package lib, which Rego joins to the first, and a rule in a file named
// like a test, which Conftest runs like any other.
func TestOnlyTheSelectorsReadTheInput(t *testing.T) {
	var libs, readers []string
	for rel, tree := range policyFiles(t) {
		switch {
		case tree.Package == "lib":
			libs = append(libs, rel)
		case isTestPackage(tree.Package):
		default:
			if n := readsInput(tree.Tree); n > 0 {
				readers = append(readers, fmt.Sprintf("%s (package %s) names the input %d time(s)", rel, tree.Package, n))
			}
		}
	}
	sort.Strings(libs)
	sort.Strings(readers)
	switch {
	case len(libs) == 0:
		t.Fatalf("no policy file in %s is package lib, so there are no selectors for any rule to read through", conftestRules)
	case len(libs) > 1:
		t.Errorf("%s all declare package lib. Rego joins them into one, so each may read the input, and the selectors are no longer in one place a guard reads. Put every selector in one file.", strings.Join(libs, ", "))
	}
	for _, r := range readers {
		t.Errorf("%s. Read the files through a selector in package lib - add one there if none picks what this needs - so the place it reads is one a guard can name.", r)
	}
}

func TestReadsInputCountsTheInputAndNothingThatSaysIt(t *testing.T) {
	tree := map[string]any{"rules": []any{
		map[string]any{"type": "ref", "value": []any{map[string]any{"type": "var", "value": "input"}, map[string]any{"type": "string", "value": "path"}}},
		map[string]any{"type": "string", "value": "input"},
		map[string]any{"type": "var", "value": "inputs"},
		map[string]any{"type": "ref", "value": []any{map[string]any{"type": "var", "value": "lib"}, map[string]any{"type": "string", "value": "objects"}}},
	}}
	if n := readsInput(tree); n != 1 {
		t.Errorf("readsInput = %d, want the one reference to input and not the string or the other names", n)
	}
}

// rulePackage is a rule's package as policy/conftest/main/main.rego collects it,
// or one of the packages beside the rules.
var rulePackage = regexp.MustCompile(`^(rules\.[a-z0-9_]+|lib|lib_test|main|main_test)$`)

// collectedPackages refuses a policy file whose package main does not
// collect: Conftest asks main alone, so a rule in any other package would
// never run, and say nothing.
func collectedPackages(t *testing.T) {
	t.Helper()
	files := 0
	for _, rel := range trackedFiles(t) {
		if !strings.HasPrefix(rel, conftestRules+"/") || !strings.HasSuffix(rel, ".rego") {
			continue
		}
		files++
		pkg := parseRego(t, rel).Package
		if pkg == "" {
			t.Errorf("%s declares no package, so nothing can say whether its rules run", rel)
			continue
		}
		if !rulePackage.MatchString(pkg) {
			t.Errorf("%s is package %s. Conftest asks main alone, and main collects the packages under rules; a rule anywhere else never runs and says nothing. Make it package rules.%s.", rel, pkg, pkg)
		}
	}
	if files == 0 {
		t.Fatalf("no policy file was found under %s, so no package was checked", conftestRules)
	}
}
