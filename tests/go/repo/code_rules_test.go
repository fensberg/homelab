package repo

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The code rules: policy/semgrep, run by Semgrep over every file of a kind
// tests/languages.yml says it parses.
//
// WHY THEY ARE NOT GO. These were Go tests that read a file's text and
// matched a pattern in it, line by line, with a regexp and a list of prefixes
// that meant "this line is a comment". Semgrep parses each file in its own
// language and matches the pattern against the code, so a comment is not
// code, a string is not a call and a call split across two lines is still one
// call. It is the tool the industry uses for this, and each rule is proved by
// examples of its own (decided 2026-10-16).
//
// WHY THIS RUNS THEM. The mutation ledger proves every guard fails when what
// it guards is broken, on a copy of the real tree. Running Semgrep from here
// keeps that proof for every rule moved: a ledger entry names this test, and
// the message of the rule that must refuse the mutation.
//
// NOT SILENCEABLE FROM INSIDE A FILE. Semgrep honours an inline ignore
// comment, which the Logic Scan lane's registry rules are silenced with, each
// naming the guard that proves it (tests/silencers.yml). An estate rule is a guard,
// and a guard a comment can switch off is a suggestion, so these run with
// --disable-nosem: a rule that is wrong is changed, with its examples.

// semgrepProgram is the code scanner, delivered by scripts/take-delivery.sh.
const semgrepProgram = "semgrep"

// semgrepRules is where the code rules live, and semgrepExamples the examples
// each is proved by, beside them.
const (
	semgrepRules    = "policy/semgrep"
	semgrepExamples = semgrepRules + "/tests"
)

// semgrepInputs is every tracked file of a kind the declaration hands to
// Semgrep, except the rules themselves: their examples are written to be
// refused, and a rule's own pattern and message are not the code it polices.
func semgrepInputs(t *testing.T) []string {
	t.Helper()
	kinds := map[string]bool{}
	for _, g := range loadLanguages(t).Governed {
		if slices.Contains(g.ParsedBy, semgrepProgram) {
			kinds[g.Kind] = true
		}
	}
	if len(kinds) == 0 {
		t.Fatal("tests/languages.yml hands no kind of file to semgrep, so the code rules would read nothing")
	}
	var files []string
	for _, rel := range trackedFiles(t) {
		if kinds[kindOf(rel)] && !strings.HasPrefix(rel, semgrepRules+"/") {
			files = append(files, rel)
		}
	}
	sort.Strings(files)
	if len(files) < 100 {
		t.Fatalf("only %d files are handed to semgrep, which is not this repository - the enumeration has stopped matching", len(files))
	}
	return files
}

// runSemgrep runs the code scanner in the repository with the given
// arguments, and gives what it wrote to standard output and whether it
// passed. It runs with a home of its own, so nothing a person's machine holds
// reaches it, and it asks the network nothing: no metrics, no version check.
func runSemgrep(t *testing.T, args ...string) ([]byte, bool) {
	t.Helper()
	if _, err := exec.LookPath(semgrepProgram); err != nil {
		t.Fatal("semgrep is not on PATH; take delivery of it: scripts/take-delivery.sh semgrep")
	}
	// One job: the ledger runs this once for each proof, side by side, and a
	// scanner that takes every core per run is that many times every core.
	args = append([]string{"scan", "--metrics=off", "--disable-version-check", "--quiet", "--json", "--jobs", "1"}, args...)
	cmd := exec.Command(semgrepProgram, args...)
	cmd.Dir = repoRoot(t)
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir(), "SEMGREP_ENABLE_VERSION_CHECK=0", "SEMGREP_SEND_METRICS=off", "NO_COLOR=1")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if _, failed := err.(*exec.ExitError); !failed {
			t.Fatalf("semgrep could not be run: %v", err)
		}
		if len(out) == 0 {
			t.Fatalf("semgrep failed and said nothing a rule could be read from:\n\n%s", stderr.String())
		}
		return out, false
	}
	return out, true
}

func TestTheCodeRulesHold(t *testing.T) {
	heavy(t, "semgrep takes about three seconds to start, and push has ten")
	if _, err := os.Stat(filepath.Join(repoRoot(t), semgrepRules)); err != nil {
		t.Fatalf("%s is not there, so no code rule runs: %v", semgrepRules, err)
	}
	args := append([]string{"--config", semgrepRules, "--disable-nosem"}, semgrepInputs(t)...)
	out, _ := runSemgrep(t, args...)
	var report struct {
		Results []struct { // matched to "results" by name
			CheckID string `json:"check_id"`
			Path    string `json:"path"`
			Start   struct {
				Line int `json:"line"`
			} `json:"start"`
			Extra struct {
				Message  string `json:"message"`
				Metadata struct {
					Presence string `json:"presence"`
				} `json:"metadata"`
			} `json:"extra"`
		}
		Errors []struct {
			Type    any    `json:"type"`
			Message string `json:"message"`
			Path    string `json:"path"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("semgrep's report could not be read: %v", err)
	}
	// A file Semgrep could not parse is a file no rule read. That is not a
	// pass for the file; it is a rule that did not run, and it is said.
	for _, e := range report.Errors {
		t.Errorf("semgrep could not read %s (%v): %s", e.Path, e.Type, strings.TrimSpace(e.Message))
	}
	var refused []string
	present := map[string]bool{}
	for _, r := range report.Results {
		if r.Extra.Metadata.Presence != "" {
			present[r.CheckID] = true
			continue
		}
		refused = append(refused, fmt.Sprintf("%s:%d: %s: %s", r.Path, r.Start.Line, strings.TrimPrefix(r.CheckID, "policy.semgrep."), strings.TrimSpace(r.Extra.Message)))
	}
	// A presence rule finds what another rule's reason rests on. Its finds are
	// not refusals, and finding none is: the reason has nothing under it.
	for id, why := range presenceRules(t) {
		if !present[id] {
			t.Errorf("%s found nothing, and %s", strings.TrimPrefix(id, "policy.semgrep."), why)
		}
	}
	sort.Strings(refused)
	if len(refused) > 0 {
		t.Errorf("a code rule refuses the repository:\n\n  %s\n\nThe rule is in %s, named before the colon; its examples in %s show what it accepts.",
			strings.Join(refused, "\n  "), semgrepRules, semgrepExamples)
	}
}

// Every rule is held to what it claims by examples of its own: Semgrep's
// --test runs each rule over the file beside it in policy/semgrep/tests and
// requires exactly the lines marked `ruleid:` to be refused.
func TestEveryCodeRuleProvesItself(t *testing.T) {
	heavy(t, "semgrep takes about three seconds to start, and push has ten")
	out, ok := runSemgrep(t, "--test", "--config", semgrepRules, semgrepExamples)
	var report struct {
		Missing []string            `json:"config_missing_tests"`
		Broken  []any               `json:"config_with_errors"`
		Results map[string]struct { // matched to "results" by name
			Checks map[string]struct {
				Passed bool `json:"passed"`
			} `json:"checks"`
		}
	}
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("semgrep's test report could not be read: %v\n\n%s", err, out)
	}
	for _, m := range report.Missing {
		t.Errorf("%s has no examples in %s, so nothing shows it refuses anything", m, semgrepExamples)
	}
	for _, b := range report.Broken {
		t.Errorf("a code rule does not load: %v", b)
	}
	checks := 0
	for file, r := range report.Results {
		for id, c := range r.Checks {
			checks++
			if !c.Passed {
				t.Errorf("%s: the rule %s does not refuse exactly the lines its examples mark", file, id)
			}
		}
	}
	if checks == 0 {
		t.Fatalf("semgrep --test ran no rule, so no rule has shown it refuses anything:\n\n%s", out)
	}
	if !ok && !t.Failed() {
		t.Errorf("semgrep --test failed and no rule says why:\n\n%s", out)
	}
}

// presenceRules is every rule whose finds are expected rather than refused,
// by the id Semgrep reports it under, with why something must be found.
func presenceRules(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, r := range codeRules(t) {
		if r.Presence != "" {
			out[strings.ReplaceAll(semgrepRules, "/", ".")+"."+r.ID] = r.Presence
		}
	}
	return out
}

// codeRule is one rule, as the file in policy/semgrep that holds it declares
// it.
type codeRule struct {
	File     string // the rule file, from the root
	ID       string // as the file writes it
	Message  string
	Presence string
	Include  []string
	Exclude  []string
}

// codeRules is every rule in policy/semgrep.
func codeRules(t *testing.T) []codeRule {
	t.Helper()
	var out []codeRule
	for _, rel := range trackedFiles(t) {
		if filepath.Dir(rel) != semgrepRules || !strings.HasSuffix(rel, ".yml") {
			continue
		}
		var doc struct {
			Rules []struct {
				ID       string `yaml:"id"`
				Message  string `yaml:"message"`
				Metadata struct {
					Presence string `yaml:"presence"`
				} `yaml:"metadata"`
				Paths struct {
					Include []string `yaml:"include"`
					Exclude []string `yaml:"exclude"`
				} `yaml:"paths"`
			} `yaml:"rules"`
		}
		if err := yaml.Unmarshal([]byte(readRepoFile(t, rel)), &doc); err != nil {
			t.Fatalf("%s is not a rule file that can be read: %v", rel, err)
		}
		for _, r := range doc.Rules {
			out = append(out, codeRule{rel, r.ID, r.Message, r.Metadata.Presence, r.Paths.Include, r.Paths.Exclude})
		}
	}
	if len(out) < 10 {
		t.Fatalf("only %d code rules were read from %s, which is not this repository - the reader has stopped matching", len(out), semgrepRules)
	}
	return out
}

// Where a rule looks is half of what it promises, and its examples cannot
// show it: Semgrep's --test runs a rule over its own example file and
// ignores the rule's paths. So nothing proved that a rule read the files it
// was written for, and one did not - the site-name rule over the workflows
// named only the files at the top of .github/workflows, and the composite
// actions in the directories below, which the guard it replaced had read,
// went unread with every check green (#779). It was found by reading.
//
// So the ledger holds the places, one proof for each:
//
//   - every place a rule includes, by a proof that breaks a file there and
//     is refused by that rule. A rule that names no place reads every file of
//     its language, and needs one proof anywhere.
//   - every place a rule excludes, by a proof that takes the exclusion out
//     and is refused by that rule: an exclusion is an exemption, and one that
//     exempts nothing is a hole waiting for something to fall in.
//   - a file nobody has written yet, by a proof that plants one: the rules
//     find what they read by its kind and its place, never from a list of the
//     files there today, and only a new file shows it.
//
// A proof is the rule's when what it requires the failure to mention is part
// of that rule's message and of no other's. The two site-name rules open with
// the same words, and the rule over programs had no proof of its own: one
// over a workflow said those words, and stood for both.
func TestEveryPlaceACodeRuleReadsIsProved(t *testing.T) {
	const harness = "TestTheCodeRulesHold"
	var proofs []mutation
	for _, m := range readLedger(t).Mutations {
		if m.Guard == harness {
			proofs = append(proofs, m)
		}
	}
	if !slices.ContainsFunc(proofs, mutation.creates) {
		t.Errorf("no proof of %s plants a file, so nothing shows a code rule reads a file that is not there today", harness)
	}
	rules := codeRules(t)
	owned := map[string][]mutation{}
	for _, m := range proofs {
		var named []string
		for _, r := range rules {
			if r.Presence == "" && strings.TrimSpace(m.Mentions) != "" && strings.Contains(r.Message, m.Mentions) {
				named = append(named, r.ID)
			}
		}
		switch {
		case len(named) > 1:
			t.Errorf("the proof that breaks %s requires %q, which %s all say, so it shows none of them reads the file", m.File, m.Mentions, strings.Join(named, " and "))
		case len(named) == 1:
			owned[named[0]] = append(owned[named[0]], m)
		}
	}
	for _, r := range rules {
		if r.Presence != "" {
			continue // it fails by itself when its place holds nothing
		}
		own := owned[r.ID]
		if len(r.Include) == 0 && len(own) == 0 {
			t.Errorf("%s: no proof is refused by %s", r.File, r.ID)
		}
		for _, place := range r.Include {
			if !slices.ContainsFunc(own, func(m mutation) bool { return semgrepPathMatches(place, m.File) }) {
				t.Errorf("%s: %s includes %s, and no proof breaks a file there that the rule refuses", r.File, r.ID, place)
			}
		}
		for _, place := range r.Exclude {
			if !slices.ContainsFunc(own, func(m mutation) bool {
				return m.File == r.File && strings.Contains(m.Find, place) && !strings.Contains(m.Replace, place)
			}) {
				t.Errorf("%s: %s excludes %s, and no proof takes the exclusion out and is refused, so nothing shows it exempts anything", r.File, r.ID, place)
			}
		}
	}
}

// semgrepPathMatches reports whether a place a rule's paths name holds a file,
// read as Semgrep reads it: as a line of a .gitignore. A leading slash, or a
// slash anywhere but the end, ties it to the root; with neither it matches at
// any depth. A trailing slash means a directory. ** stands for any number of
// directories, and a pattern that matches a directory matches what is in it.
func semgrepPathMatches(pattern, rel string) bool {
	dirOnly := strings.HasSuffix(pattern, "/")
	pattern = strings.TrimSuffix(pattern, "/")
	anchored := strings.Contains(pattern, "/")
	pat := strings.Split(strings.TrimPrefix(pattern, "/"), "/")
	parts := strings.Split(rel, "/")
	for end := len(parts); end >= 1; end-- {
		if dirOnly && end == len(parts) {
			continue // the file itself is not a directory
		}
		for start := 0; start < end && (start == 0 || !anchored); start++ {
			if segmentsMatch(pat, parts[start:end]) {
				return true
			}
		}
	}
	return false
}

// segmentsMatch reports whether a pattern's segments match a path's, one
// for one, with ** taking as many as it needs.
func segmentsMatch(pat, parts []string) bool {
	if len(pat) == 0 {
		return len(parts) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(parts); i++ {
			if segmentsMatch(pat[1:], parts[i:]) {
				return true
			}
		}
		return false
	}
	if len(parts) == 0 {
		return false
	}
	ok, err := path.Match(pat[0], parts[0])
	return err == nil && ok && segmentsMatch(pat[1:], parts[1:])
}

func TestSemgrepPathsAreReadAsGitignoreLines(t *testing.T) {
	for _, c := range []struct {
		pattern, rel string
		want         bool
	}{
		{"/.github/workflows/**/*.yml", ".github/workflows/pr.yml", true},
		{"/.github/workflows/**/*.yml", ".github/workflows/mobilize/action.yml", true},
		{"/.github/workflows/*.yml", ".github/workflows/mobilize/action.yml", false},
		{"/.github/workflows/**/*.yml", "x/.github/workflows/pr.yml", false},
		{"/taskfile.yml", "taskfile.yml", true},
		{"/taskfile.yml", "apps/taskfile.yml", false},
		{"taskfile.yml", "apps/taskfile.yml", true},
		{"*_test.go", "scripts/contractor/a_test.go", true},
		{"*_test.go", "scripts/contractor/a.go", false},
		{"/scripts/contractor/", "scripts/contractor/internal/phases/destroy.go", true},
		{"/scripts/contractor/", "scripts/contractorish/main.go", false},
		{"/scripts/contractor/", "scripts/contractor", false},
		{"/tests/mutations/", "tests/mutations/a.yml", true},
		{"/tests/mutations.yml", "tests/mutations/a.yml", false},
		{"*.md", "docs/a/b.md", true},
		{"scripts/contractor/", "a/scripts/contractor/b.go", false},
		{"[", "[", false},
	} {
		if got := semgrepPathMatches(c.pattern, c.rel); got != c.want {
			t.Errorf("semgrepPathMatches(%q, %q) = %v, want %v", c.pattern, c.rel, got, c.want)
		}
	}
}

// A rule's examples show what it refuses, and Semgrep holds it to refusing
// exactly the lines they mark. That is half an oracle: the other half is what
// it must let through, and a rule with no example of that has never been
// shown a line that looks like the fault and is not. The pattern `.*` passes
// an example made only of faults. So every rule that refuses shows, in its
// examples, at least one line marked `ok:` with its id - the near miss it was
// written to tell apart.
func TestEveryCodeRuleShowsWhatItAccepts(t *testing.T) {
	accepted := map[string]bool{}
	for _, rel := range trackedFiles(t) {
		if filepath.Dir(rel) != semgrepExamples {
			continue
		}
		stem := strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
		for _, line := range strings.Split(readRepoFile(t, rel), "\n") {
			for _, id := range acceptedIn(line) {
				accepted[semgrepRules+"/"+stem+".yml "+id] = true
			}
		}
	}
	for _, r := range codeRules(t) {
		if r.Presence == "" && !accepted[r.File+" "+r.ID] {
			t.Errorf("%s: %s has no example marked `ok: %s`, so nothing shows what it lets through", r.File, r.ID, r.ID)
		}
	}
}

// okMark is Semgrep's annotation for a line a rule must not refuse.
var okMark = regexp.MustCompile(`(?:^|[^a-z])ok:\s*([a-z0-9][a-z0-9, -]*)`)

// acceptedIn is the rule ids a line of an example marks as not to be refused.
func acceptedIn(line string) []string {
	var ids []string
	for _, m := range okMark.FindAllStringSubmatch(line, -1) {
		for _, id := range strings.Split(m[1], ",") {
			if id = strings.TrimSpace(id); id != "" {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

func TestAcceptedInReadsSemgrepsOkMark(t *testing.T) {
	for _, c := range []struct {
		line string
		want []string
	}{
		{"# ok: a-rule", []string{"a-rule"}},
		{"\t// ok: a-rule, b-rule", []string{"a-rule", "b-rule"}},
		{"# ruleid: a-rule", nil},
		{"# todook: a-rule", nil},
		{"x := 1 // ok:a-rule", []string{"a-rule"}},
		{"nothing marked here", nil},
	} {
		if got := acceptedIn(c.line); !slices.Equal(got, c.want) {
			t.Errorf("acceptedIn(%q) = %q, want %q", c.line, got, c.want)
		}
	}
}
