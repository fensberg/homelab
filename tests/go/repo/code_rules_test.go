package repo

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	for _, rel := range trackedFiles(t) {
		if filepath.Dir(rel) != semgrepRules || !strings.HasSuffix(rel, ".yml") {
			continue
		}
		var doc struct {
			Rules []struct {
				ID       string `yaml:"id"`
				Metadata struct {
					Presence string `yaml:"presence"`
				} `yaml:"metadata"`
			} `yaml:"rules"`
		}
		if err := yaml.Unmarshal([]byte(readRepoFile(t, rel)), &doc); err != nil {
			t.Fatalf("%s is not a rule file that can be read: %v", rel, err)
		}
		for _, r := range doc.Rules {
			if r.Metadata.Presence != "" {
				out[strings.ReplaceAll(semgrepRules, "/", ".")+"."+r.ID] = r.Metadata.Presence
			}
		}
	}
	return out
}
