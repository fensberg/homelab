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
	blind, err := json.Marshal(map[string]any{"account": "it adds two numbers", "findings": []any{}})
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
