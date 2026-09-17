package repo

import (
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
// the pull request: the Test lane runs this package without -short, and this
// refuses a workflow that passes it, because a -short in CI would turn every
// heavy guard into a green skip with nothing left anywhere that runs it.
func TestHeavyGuardsRunOnEveryPullRequest(t *testing.T) {
	body := intendedWorkflow(t, "pr-validation.yml")

	full := regexp.MustCompile(`(?m)working-directory: tests/go\n\s+run: go test \./\.\.\.\s*$`)
	if !full.MatchString(body) {
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
}
