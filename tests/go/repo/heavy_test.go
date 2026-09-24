package repo

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// heavy skips a guard under `go test -short`, which is what the pre-push hook
// runs, and says why the guard can wait for the pull request.
//
// THE BUDGET. Committing and pushing together should take under ten seconds,
// and a hook nobody waits on is a hook nobody bypasses. The full suite took
// 160 seconds at push, and two checks on the checks were 107 of them. So a
// push runs only what cannot be undone once it is public - a real name, a
// secret, an unsigned commit - and the guards that answer in milliseconds.
// CI's Test lane runs everything, without -short, on every pull request.
//
// WHAT MAY BE HEAVY. A guard whose failure a later push can fix, and which
// costs whole seconds. Never one that stops something being published that
// cannot be unpublished - a real name or address in a fixture or a pasted
// transcript is public the moment the push lands, and no later push takes it
// back.
func heavy(t *testing.T, why string) {
	t.Helper()
	if why == "" {
		t.Fatal("heavy needs the reason this guard can wait for the pull request")
	}
	if testing.Short() {
		t.Skip("heavy, so it runs on the pull request rather than at push: " + why)
	}
}

// The skip above is honest only while something runs the heavy guards. It is
// the pull request: the test lanes run this package without -short, and this
// refuses a workflow that passes it, because a -short in CI would turn every
// heavy guard into a green skip with nothing left anywhere that runs it.
//
// The tier runs in two lanes, so "runs the whole package" is now two
// commands: the invariants lane runs `go test ./...` skipping the meta-tests,
// and the meta lane runs only them. Both name them through META_TESTS, and
// that is where this looks for the gap. A name in it that matches no test
// would be skipped by one lane and selected by nothing in the other - `-run`
// with no match passes with "no tests to run" - so every name must be a test
// that exists.
func TestHeavyGuardsRunOnEveryPullRequest(t *testing.T) {
	body := intendedWorkflow(t, "pr-validation.yml")

	full := regexp.MustCompile(`(?m)working-directory: tests/go\n(?:\s+#.*\n)*\s+run: go test \./\.\.\.( -skip "\$META_TESTS")?\s*$`)
	m := full.FindStringSubmatch(body)
	if m == nil {
		t.Error("pr-validation.yml no longer runs `go test ./...` in tests/go, so the guards " +
			"heavy() skips at push run nowhere - every one of them is switched off")
	}
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "go test") && strings.Contains(line, "-short") {
			t.Errorf("pr-validation.yml runs Go tests with -short: %s\n\n"+
				"-short is the push budget, and the pull request is where the heavy guards "+
				"it skips are supposed to run", strings.TrimSpace(line))
		}
	}
	if m == nil || m[1] == "" {
		return
	}

	// The full run skips the meta-tests, so they must run somewhere else,
	// selected by the same variable.
	if !regexp.MustCompile(`(?m)run: go test \./repo/ -run "\$META_TESTS"\s*$`).MatchString(body) {
		t.Error("pr-validation.yml skips $META_TESTS in the full run of tests/go and no lane " +
			"runs them with `go test ./repo/ -run \"$META_TESTS\"`, so the mutation ledger and " +
			"the comment-stripping re-run are switched off")
	}
	decl := regexp.MustCompile(`(?m)^  META_TESTS: "\^\(([A-Za-z0-9_|]+)\)\$"\s*$`).FindStringSubmatch(body)
	if decl == nil {
		t.Fatal("pr-validation.yml uses $META_TESTS but does not declare it at the top as " +
			"META_TESTS: \"^(TestA|TestB)$\", so this cannot check that each name is a real test")
	}
	defined := map[string]bool{}
	for _, f := range repoTestFuncs(t) {
		defined[f] = true
	}
	for _, name := range strings.Split(decl[1], "|") {
		if !defined[name] {
			t.Errorf("META_TESTS names %s, which is not a test in tests/go/repo.\n\n"+
				"The invariants lane skips it and the meta lane's -run matches nothing, which "+
				"passes with \"no tests to run\". Rename it here to match the test.", name)
		}
	}
}

// repoTestFuncs lists every top-level test declared in this package.
func repoTestFuncs(t *testing.T) []string {
	t.Helper()
	dir := filepath.Join(repoRoot(t), "tests", "go", "repo")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	decl := regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]+)\(t \*testing\.T\)`)
	var out []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range decl.FindAllSubmatch(body, -1) {
			out = append(out, string(m[1]))
		}
	}
	if len(out) == 0 {
		t.Fatal("found no tests in tests/go/repo, so no name could be checked against them")
	}
	return out
}
