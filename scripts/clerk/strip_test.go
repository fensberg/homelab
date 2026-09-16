package main

import (
	"strings"
	"testing"
)

const lyingGo = `package thing

import "fmt"

// Retries three times before giving up, with a backoff between attempts.
func try() {
	fmt.Println("once")
}

// The separator is a slash, as in http://example.com/path.
const sep = "//"
`

// The blind pass must not be told what the code is supposed to do.
func TestGoCommentsAreTakenAwayFromTheCode(t *testing.T) {
	code, prose, ok := split("thing.go", lyingGo)
	if !ok {
		t.Fatal("Go should split")
	}
	if strings.Contains(code, "Retries three times") {
		t.Error("the claim reached the blind pass")
	}
	if !strings.Contains(prose, "Retries three times") {
		t.Error("the claim was lost instead of set aside")
	}
	if !strings.Contains(code, `fmt.Println("once")`) {
		t.Errorf("the code did not survive:\n%s", code)
	}
}

// A citation is only checkable if the line numbers are the file's own.
//
// Deleting the commentary moves every line after it, so a finding citing line
// 40 of what the model read points at a different line 40 in the file the
// operator opens. Blanking keeps them the same.
func TestBlankingKeepsEveryLineNumberTrue(t *testing.T) {
	for _, c := range []struct{ name, path, body string }{
		{"go", "thing.go", lyingGo},
		{"yaml", "ci.yml", "# a claim\njobs:\n  build:\n    timeout-minutes: 10\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			code, _, _ := split(c.path, c.body)
			was := strings.Count(c.body, "\n")
			now := strings.Count(code, "\n")
			if was != now {
				t.Fatalf("the file had %d newlines and the blind pass sees %d", was, now)
			}
			for i, line := range strings.Split(c.body, "\n") {
				if strings.Contains(line, "timeout-minutes") || strings.Contains(line, `Println("once")`) {
					got := strings.Split(code, "\n")[i]
					if strings.TrimSpace(got) != strings.TrimSpace(line) {
						t.Errorf("line %d moved: was %q, now %q", i+1, line, got)
					}
				}
			}
		})
	}
}

// Parsing rather than pattern-matching, and this is why.
//
// A regular expression over "//" removes the slashes inside the URL and inside
// the string literal, corrupting the code the blind pass has to reason about -
// and then reports a finding about the corruption.
func TestASlashInsideAStringIsNotACommentMarker(t *testing.T) {
	code, _, _ := split("thing.go", lyingGo)
	if !strings.Contains(code, `"//"`) {
		t.Errorf("the string literal was mangled as if it were a comment:\n%s", code)
	}
}

func TestHashCommentsAreTakenFromYAML(t *testing.T) {
	const y = "# every job is bounded\njobs:\n  build:\n    timeout-minutes: 10\n"
	code, prose, ok := split("ci.yml", y)
	if !ok {
		t.Fatal("yaml should split")
	}
	if strings.Contains(code, "bounded") {
		t.Error("the claim reached the blind pass")
	}
	if !strings.Contains(prose, "bounded") || !strings.Contains(code, "timeout-minutes") {
		t.Errorf("split wrong:\ncode=%q\nprose=%q", code, prose)
	}
}

// A trailing marker is left alone on purpose.
//
// Taking it would mangle a URL, a colour or a string more often than it would
// find a real comment, and the blind pass reasons about the code.
func TestATrailingMarkerIsLeftAlone(t *testing.T) {
	code, _, _ := split("x.yml", "url: https://example.com/a#b\n")
	if !strings.Contains(code, "example.com/a#b") {
		t.Errorf("a URL was cut at a hash: %q", code)
	}
}

// A record is a claim about the estate, not a thing that runs.
func TestMarkdownIsAllProse(t *testing.T) {
	code, prose, ok := split("docs/epochs/01.md", "# Epoch\n\nThe button is idempotent.\n")
	if ok {
		t.Error("markdown should not be offered to the blind pass as code")
	}
	if code != "" || !strings.Contains(prose, "idempotent") {
		t.Errorf("code=%q prose=%q", code, prose)
	}
}

// Unparsable Go still gets read, with a weaker separation rather than none.
func TestBrokenGoFallsBackRatherThanRefusing(t *testing.T) {
	code, prose, ok := split("broken.go", "package x\n// a claim\nfunc (\n")
	if !ok {
		t.Fatal("should still split")
	}
	if strings.Contains(code, "a claim") || !strings.Contains(prose, "a claim") {
		t.Errorf("fallback did not separate:\ncode=%q prose=%q", code, prose)
	}
}

// Every language this repository writes must actually have its comments taken
// away, and the marker has to match what the files really use.
//
// THE FAILURE THIS CATCHES IS SILENT. Picking the wrong marker does not error
// and does not produce an empty result: nothing matches, every comment is
// treated as code, and the blind pass is handed a fully-commented file while
// believing it was given a bare one. Both halves of the clerk go quiet at once
// - the first pass reads the reasoning it was designed not to see, and the
// prose side is empty so the comparison pass has nothing of that file to check.
//
// It happened. `.tf` and `.hcl` were listed with the `//` languages, and this
// repository writes HCL with `#` - 879 whole-line `#` comments in
// management/cluster and not a single `//`. Every OpenTofu file the clerk has
// ever read was read with its commentary attached.
func TestSplitTakesCommentsAwayInEveryLanguageWritten(t *testing.T) {
	for _, tc := range []struct {
		name    string
		path    string
		comment string
		code    string
	}{
		// The one that was wrong. HCL accepts three comment forms and this
		// repository uses the first essentially always.
		{"HCL with a hash", "management/cluster/talos.tf", "# why this resource exists", `resource "talos_machine_secrets" "this" {}`},
		{"HCL with slashes", "management/cluster/talos.tf", "// also legal HCL", `resource "talos_machine_secrets" "this" {}`},
		{"YAML", "clusters/management/infrastructure/controllers/openebs.yaml", "# why this value", "chart: openebs"},
		{"shell", "scripts/install-dependencies.sh", "# why this step", "set -euo pipefail"},
		{"Go", "scripts/clerk/strip.go", "// why this function", "package main"},
		{"TypeScript", "web/app.ts", "// why this export", "export const a = 1;"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := tc.comment + "\n" + tc.code + "\n"
			code, prose, ok := split(tc.path, body)
			if !ok {
				t.Fatalf("split(%s) reported the file as entirely prose", tc.path)
			}
			if !strings.Contains(prose, strings.TrimLeft(tc.comment, "#/ ")) {
				t.Errorf("the comment never reached the prose side.\n  prose: %q", prose)
			}
			if strings.Contains(code, "why this") || strings.Contains(code, "also legal") {
				t.Errorf(`the comment is still in the code the blind pass reads:

  %q

That pass exists to read the code WITHOUT being told what it is for. Handed the
commentary, it reads the code looking for what the comment promised, and the
comparison pass then has nothing of this file to check - both halves go quiet
at once, and neither reports anything.`, code)
			}
			if !strings.Contains(code, strings.Fields(tc.code)[0]) {
				t.Errorf("the code itself was blanked out:\n  %q", code)
			}
		})
	}
}

// A `#` inside a YAML block scalar is data, and must not reach the prose side.
//
// THE CASE THIS IS BUILT FROM. The clerk reported, on #254:
//
//	tests/mutations.yml:49 - The commentary mentions 'context that does not
//	exist either', but the account does not mention any such context.
//
// The flagged text is one context line of a deliberately unappliable diff,
// carried as the `create:` payload of a ledger entry. Nothing in any account
// supports it because nothing should - describing a workflow line that does not
// exist is the whole point of that fixture.
//
// Deterministic, and it fires on every pull request that adds a guard.
func TestAHashInsideAYAMLBlockScalarIsNotCommentary(t *testing.T) {
	body := `mutations:
  - guard: TestEveryOutstandingPatchStillApplies
    file: .github/patches/a-patch-that-cannot-apply.patch
    create: |
      diff --git a/.github/workflows/codeql.yml b/.github/workflows/codeql.yml
      @@ -1,3 +1,3 @@
      -name: "A line this workflow has never contained"
       # context that does not exist either
    mentions: "does not apply"

# A real comment, outside the scalar.
floor: 62
`

	code, prose, ok := split("tests/mutations.yml", body)
	if !ok {
		t.Fatal("a YAML file should go to both sides")
	}

	if strings.Contains(prose, "context that does not exist either") {
		t.Errorf(`the diff's context line reached the prose side:

%s
It is the payload of a fixture whose purpose is to describe something absent,
so the comparison pass is asked to find an account supporting a claim that is
not a claim. It cannot, and reports a contradiction every single run.`, prose)
	}
	if !strings.Contains(code, "context that does not exist either") {
		t.Error("the diff's context line was blanked out of the code side, so the " +
			"blind pass can no longer see what the fixture actually contains")
	}
	if !strings.Contains(prose, "A real comment, outside the scalar.") {
		t.Errorf("a genuine comment outside the block scalar stopped being "+
			"recognised as commentary, which switches the comparison pass off for "+
			"the rest of the file:\n\n%s", prose)
	}
	if strings.Contains(code, "A real comment, outside the scalar.") {
		t.Error("a genuine comment survived into the code side, so the blind pass " +
			"is no longer blind")
	}
	if lines(code) != lines(body) {
		t.Errorf("the code side has %d lines and the file has %d. Every citation "+
			"the model makes is a line number, and they have to mean the same thing "+
			"in both.", lines(code), lines(body))
	}
}

// Block scalar spelling varies, and every spelling has to be recognised.
//
// A header this misses is a region left unprotected, which is the original bug
// arriving through a different door - and it would do so silently, because an
// unprotected region looks exactly like a file with no block scalars in it.
func TestEveryBlockScalarSpellingIsRecognised(t *testing.T) {
	for _, header := range []string{"|", "|-", "|+", ">", ">-", ">+", "|2", "|-2", "|2-", "| # trailing"} {
		t.Run(header, func(t *testing.T) {
			body := "root:\n  key: " + header + "\n    # payload\n  next: value\n"
			_, prose, _ := split("x.yml", body)
			if strings.Contains(prose, "payload") {
				t.Errorf("a %q block scalar's contents reached the prose side:\n%s", header, prose)
			}
		})
	}

	// And the converse: what looks like a header but is not must not protect
	// anything, or a real comment stops being read as one.
	body := "key: value | not a header\n  # a real comment\n"
	_, prose, _ := split("x.yml", body)
	if !strings.Contains(prose, "a real comment") {
		t.Errorf("a pipe in the middle of a value was read as a block scalar header, "+
			"so the comment after it was protected and never reached the prose side:\n%s", prose)
	}
}

// The scalar ends where the indentation says it does.
//
// Protecting too much is the same defect pointed the other way: every comment
// after a block scalar would stop being commentary, and the comparison pass
// would go quiet for the rest of the file without saying so.
func TestABlockScalarEndsAtTheFirstLineThatDedents(t *testing.T) {
	body := `first:
  why: |
    # inside
    still inside

    blank lines do not end it
    # also inside
  # outside again
second: value
# and at the root
`
	_, prose, _ := split("x.yml", body)
	for _, inside := range []string{"# inside", "# also inside"} {
		if strings.Contains(prose, inside) {
			t.Errorf("%q was treated as commentary:\n%s", inside, prose)
		}
	}
	for _, outside := range []string{"# outside again", "# and at the root"} {
		if !strings.Contains(prose, outside) {
			t.Errorf(`%q was not treated as commentary:

%s
The block scalar has swallowed the rest of the file, so every comment after it
is invisible to the comparison pass - which reports nothing and looks clean.`, outside, prose)
		}
	}
}

// A `#` inside a shell heredoc is data too.
//
// Named in #255 alongside the YAML case and the same shape: this repository
// writes heredocs full of YAML and of shell, and `.sh` went through the
// identical line-prefix stripper.
func TestAHashInsideAShellHeredocIsNotCommentary(t *testing.T) {
	body := `#!/usr/bin/env bash
# a real comment
cat <<'EOF' > out.yml
# this is data
key: value
EOF
# another real comment
cat <<-INDENTED
	# also data
	INDENTED
`
	code, prose, _ := split("x.sh", body)

	for _, data := range []string{"# this is data", "# also data"} {
		if strings.Contains(prose, data) {
			t.Errorf("%q reached the prose side from inside a heredoc:\n%s", data, prose)
		}
		if !strings.Contains(code, data) {
			t.Errorf("%q was blanked out of the code side, so the blind pass cannot "+
				"see what the script actually writes", data)
		}
	}
	for _, comment := range []string{"# a real comment", "# another real comment"} {
		if !strings.Contains(prose, comment) {
			t.Errorf("%q stopped being recognised as commentary:\n%s", comment, prose)
		}
	}
}

func lines(s string) int { return strings.Count(s, "\n") }
