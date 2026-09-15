package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// A documentation-only change has no code to write an account of.
//
// This is the case that took the lane red on #241: five epoch records and
// nothing else. `split` sends Markdown wholly to the prose side, so the bundle
// reaches snagPass with empty code, the first pass was asked about nothing, and
// the model answered - correctly - with no account. parseBlind reported that as
// a failure, so a pull request the clerk had no business reading at all failed
// the check rather than passing it.
//
// The handler fails the test if it is reached, because "did not call the model"
// is the actual property. A guard that returned early but still spent a request
// would satisfy a weaker assertion while wasting a call from a daily budget
// measured in tens.
func TestSnagAsksNothingWhenThereIsNoCode(t *testing.T) {
	a, _ := testAsker(t, func(http.ResponseWriter, *http.Request) {
		t.Error("the clerk called the model for a change that contained no code")
	})

	found, caveat, err := snagPass(a, &bundle{prose: "# a record\n\nclaims about the estate\n"})
	if err != nil {
		t.Fatalf("a change with no code is not an error, it is nothing to do: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("findings raised against a change with no code: %v", found)
	}
	if caveat == "" {
		t.Error("nothing was reviewed and the note would have said 'nothing to raise'")
	}
}

// The inverse, so the guard above cannot be over-broad.
//
// A guard that always returned early would pass the test above and silently
// disable the clerk. This one fails if snagPass ever stops reading real code.
func TestSnagStillReadsCodeWhenThereIsSome(t *testing.T) {
	var asked int
	blind, err := json.Marshal(map[string]any{
		"accounts": map[string]string{"add.go": "it adds two numbers"},
		"findings": []any{},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	reply, err := json.Marshal(map[string]any{
		"candidates": []any{map[string]any{
			"content": map[string]any{"parts": []any{map[string]string{"text": string(blind)}}},
		}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	a, _ := testAsker(t, func(w http.ResponseWriter, _ *http.Request) {
		asked++
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write(reply)
	})

	_, caveat, err := snagPass(a, &bundle{code: "1: func add(a, b int) int { return a + b }\n"})
	if err != nil {
		t.Fatalf("snagPass: %v", err)
	}
	if asked == 0 {
		t.Fatal("the clerk read no code even though the change contained some")
	}
	if !strings.Contains(caveat, "nothing was written") {
		t.Errorf("code with no commentary should say only the soundness pass ran, got %q", caveat)
	}
}

// A file's commentary is only ever compared against an account of that file.
//
// THE FAILURE THIS REPLACES. On #317 the clerk produced five findings, all
// false, all the same shape: a comment in one file held against an account of
// a different one. Read the connectives -
//
//	openebs.yaml:150 - The commentary states that localpv.privileged: true is
//	read by templates/psp.yaml, but the account states that volume snapshots
//	are a CSI feature...
//
// Those are not contradictions, they are two statements about different things.
// Three of the five paired a comment against a generic description of the
// project, and against a blurb every specific claim looks like an addition, so
// the contradiction is manufactured at the right altitude from the wrong
// document. A prompt rule could not reach it: the model was obeying the rule
// that says quote the account, and it did quote one.
func TestEachFilesCommentaryIsPairedWithItsOwnAccount(t *testing.T) {
	b := &bundle{
		included: []string{"health.go", "02-abstraction.md"},
		proseOf: map[string]string{
			"health.go":         "the storage provisioner is a Deployment rather than a DaemonSet",
			"02-abstraction.md": "the nodes are online in the console and answer nothing on the data plane",
		},
		codedOf: map[string]bool{"health.go": true},
	}
	accounts := map[string]string{"health.go": "a table of health checks"}

	paired, uncompared := pair(b, accounts)

	if !strings.Contains(paired, "health.go") {
		t.Error("the file that contributed code was not compared at all")
	}
	if !strings.Contains(paired, "a table of health checks") {
		t.Error("the account of health.go was not paired with its own commentary")
	}
	if strings.Contains(paired, "the nodes are online in the console") {
		t.Errorf(`the epoch record's prose was sent to the comparison pass:

%s
It contributed no code, so no account describes it. Pairing it with the account
of the Go beside it is how five false findings were produced on one change.`, paired)
	}
	if len(uncompared) != 1 || uncompared[0] != "02-abstraction.md" {
		t.Errorf("the uncompared file was not reported: %v", uncompared)
	}

	// And the run has to say so. "Checked and consistent" and "never checked"
	// are otherwise the same silence.
	note := uncomparedNote(uncompared)
	if !strings.Contains(note, "02-abstraction.md") {
		t.Errorf("the run note does not name the file that was not compared: %q", note)
	}
	if uncomparedNote(nil) != "" {
		t.Error("a run that compared everything should add no caveat")
	}
}

// A change whose only commentary is uncomparable spends no second call.
//
// The clerk's models are on a free tier measured in tens of calls a day, so a
// call that cannot produce an answer is a real cost. It is also the honest
// outcome: there is nothing to compare, and asking anyway invites exactly the
// manufacture this change removes.
func TestNothingComparableAsksNoSecondQuestion(t *testing.T) {
	var asked int
	blind, err := json.Marshal(map[string]any{
		"accounts": map[string]string{"add.go": "it adds two numbers"},
		"findings": []any{},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	reply, err := json.Marshal(map[string]any{
		"candidates": []any{map[string]any{
			"content": map[string]any{"parts": []any{map[string]string{"text": string(blind)}}},
		}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	a, _ := testAsker(t, func(w http.ResponseWriter, _ *http.Request) {
		asked++
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write(reply)
	})

	_, caveat, err := snagPass(a, &bundle{
		code:     "1: func add(a, b int) int { return a + b }\n",
		prose:    "=== written about notes.md ===\nclaims about the estate\n",
		included: []string{"notes.md"},
		proseOf:  map[string]string{"notes.md": "claims about the estate"},
	})
	if err != nil {
		t.Fatalf("snagPass: %v", err)
	}
	if asked != 1 {
		t.Errorf("the clerk made %d calls; the comparison pass had nothing to compare "+
			"and should not have been asked", asked)
	}
	if !strings.Contains(caveat, "notes.md") {
		t.Errorf("the run said nothing about the file it could not compare: %q", caveat)
	}
}
