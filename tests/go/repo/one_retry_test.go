package repo

import (
	"strings"
	"testing"
)

// There is one retry in this estate, and a second one cannot be written.
//
// WHY THIS SHAPE. There were three: a hand-rolled `for attempt in 1 2 3` loop
// in expediter.yml (now expedite.yml), the bounded HTTP retry in scripts/clerk/llm.go, and the
// shell helper. Three implementations of one policy is three places for it to
// drift, and the fourth would have been written the same way the third was -
// by somebody who did not know the other two existed.
//
// So this does not check that the three agree. It DISCOVERS retry-shaped loops
// across the whole repository and refuses any that is not one of the two
// declared implementations. A caller writing a fourth is told where the
// existing one is, at the moment they write it, without anybody having to
// remember to extend a list of files to look in.
//
// WHY TWO IMPLEMENTATIONS AND NOT ONE. scripts/retry.sh wraps a command and can
// see only its exit status. scripts/clerk/llm.go retries an HTTP call and can
// see the STATUS CODE, so it retries a 429 or a 5xx and returns immediately on
// any other 4xx - "a rejected key is rejected on the third attempt too". That
// is strictly more precise and a shell wrapper cannot express it. Collapsing
// them would mean either losing that precision or shelling out from inside an
// HTTP client, and both are worse than two implementations of one POLICY,
// which is what is actually shared: bounded, never a verdict, every attempt
// reported.

// A loop that runs something repeatedly until it works. Deliberately broad -
// the point is to catch a shape somebody invents, not one they copy. Each is
// held to one line: spaces are [ \t], never a newline.
var retryShapes = []string{
	`for[ \t]+attempt[ \t]+in\b`,
	`for[ \t]+(?:i|n|try|retry|attempts?)[ \t]+in[ \t]+(?:1[ \t]+2|\$\(seq)`,
	`while[ \t]+[^\n]*\b(?:retry|retries|attempt)\b[^\n]*;[ \t]*do`,
	`\battempt[ \t]*(?:\+\+|\+=[ \t]*1|=[ \t]*attempt[ \t]*\+[ \t]*1)`,
	`for[ \t]+attempt[ \t]*:?=[ \t]*1;`,
}

// retryRule is the shapes as one Semgrep rule over lines that are not
// comments - prose about a retry is not a retry, and this repository explains
// its mechanisms at length and half those paragraphs say the word - giving
// back the line it found.
func retryRule() map[string]any {
	return map[string]any{
		"id": "a-retry", "languages": []string{"generic"}, "severity": "INFO",
		"message": "$LINE", "metadata": map[string]any{"about": "retry"},
		"pattern-regex": `(?mi)^(?![ \t]*(?:#|//|\*))[ \t]*(?P<LINE>[^\n]*?(?:` + strings.Join(retryShapes, "|") + `)[^\n]*)`,
	}
}

// The two places a retry is allowed to be implemented, and the reason each
// exists. Anything else is a third implementation.
var declaredRetries = map[string]string{
	"scripts/retry.sh":                "the shell helper: wraps a command, sees its exit status",
	"scripts/clerk/llm.go":            "the HTTP retry: sees the status code, so it can tell a 429 from a 401",
	"tests/go/repo/one_retry_test.go": "this file, which carries the shapes in order to refuse them",
	"tests/go/repo/retry_test.go":     "the helper's own tests",
}

func TestThereIsOnlyOneRetryAndANewOneIsRefused(t *testing.T) {
	heavy(t, "semgrep reads every script and workflow for a retry, a few seconds")

	// Every file that could hold one: shell, Go, workflows, the taskfile.
	candidates := tracked(t, func(rel string) bool {
		if strings.HasPrefix(rel, "node_modules/") {
			return false
		}
		return strings.HasSuffix(rel, ".sh") || strings.HasSuffix(rel, ".go") ||
			strings.HasPrefix(rel, ".github/workflows/") || rel == "taskfile.yml" ||
			strings.HasPrefix(rel, "githooks/")
	})

	const atLeastFiftyCandidates = 50
	if len(candidates) < atLeastFiftyCandidates {
		t.Fatalf(`only %d file(s) were searched for a retry loop, and this repository has
far more that could hold one.

The walk has stopped matching, so a hand-rolled retry could be added anywhere it
no longer looks - which is the same green as there being none.`, len(candidates))
	}

	var searched []string
	for _, rel := range candidates {
		if _, declared := declaredRetries[rel]; !declared {
			searched = append(searched, rel)
		}
	}
	for _, f := range scanText(t, repository(t), []map[string]any{retryRule()}, searched) {
		t.Errorf(`%s hand-rolls a retry:

    %s

There is already one, and a second is a second place for the policy to drift -
bounded attempts, never a verdict, every attempt reported. Use it:

    scripts/retry.sh <attempts> <delay-seconds> <command> [args...]

If what you need is an HTTP retry that can tell a 429 from a 401, that is
scripts/clerk/llm.go and it is the other declared implementation. If it is
genuinely neither, add this file to the declared list with one line saying
what it sees that the other two cannot.`, f.Path, strings.TrimSpace(f.Text))
	}
	if len(searched) == 0 {
		t.Fatal("every candidate file was declared, so this test examined nothing")
	}
}

// Every declared implementation still exists.
//
// A declaration for a file that is gone makes the list longer than the truth,
// and the next person reading it believes there are more retries here than
// there are.
func TestEveryDeclaredRetryStillExists(t *testing.T) {
	for path, why := range declaredRetries {
		if strings.TrimSpace(why) == "" {
			t.Errorf("%s is declared with no reason, so nobody can tell whether it "+
				"should still be there", path)
		}
		if !repository(t).Exists(path) {
			t.Errorf("%s is declared as a retry implementation and is gone", path)
		}
	}
}
