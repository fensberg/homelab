package main

import "testing"

// The decision, as a table. Every git and GitHub call is out of the way, so
// what is being tested is the rule rather than a fixture repository.
func TestFinishedNamesWhichEvidenceDecidedIt(t *testing.T) {
	for _, tc := range []struct {
		name       string
		branch     string
		current    string
		track      string
		insideMain bool
		mergedPR   bool
		wantWhy    string
		wantDone   bool
	}{
		{
			name:   "the branch you are standing on is never collected",
			branch: "feat/in-progress", current: "feat/in-progress",
			track: "[gone]", insideMain: true, mergedPR: true,
			wantWhy: "", wantDone: false,
		},
		{
			name:   "main is never collected",
			branch: "main", current: "feat/other",
			track: "[gone]", insideMain: true, mergedPR: true,
			wantWhy: "", wantDone: false,
		},
		{
			name:   "a deleted upstream means it was merged and tidied",
			branch: "fix/landed", current: "main", track: "[gone]",
			wantWhy: "upstream gone", wantDone: true,
		},
		{
			name:   "a tip already in main is redundant whatever its upstream says",
			branch: "fix/rebased", current: "main", track: "", insideMain: true,
			wantWhy: "inside main", wantDone: true,
		},
		{
			// The case that matters. A squash-merged branch has a live-looking
			// upstream reference, is not an ancestor of main, and is finished.
			// Nothing local can tell; GitHub can.
			name:   "a squash-merged branch is only identifiable from its pull request",
			branch: "fix/squashed", current: "main",
			track: "[ahead 3, behind 40]", insideMain: false, mergedPR: true,
			wantWhy: "pull request merged", wantDone: true,
		},
		{
			name:   "work that landed nowhere is kept",
			branch: "spike/abandoned", current: "main",
			track: "", insideMain: false, mergedPR: false,
			wantWhy: "not landed anywhere", wantDone: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			why, done := finished(tc.branch, tc.current, tc.track, tc.insideMain, tc.mergedPR)
			if done != tc.wantDone {
				t.Errorf("collect=%v, want %v - %q", done, tc.wantDone, why)
			}
			if why != tc.wantWhy {
				t.Errorf("reason %q, want %q. The reason is what a person reads while a hundred branches disappear.", why, tc.wantWhy)
			}
		})
	}
}

// The protections are absolute, and worth asserting separately from the table
// because the table could be edited into agreeing with a broken rule.
//
// A checkout where every branch looks finished is exactly the state this runs
// in, so "current" and "main" being refused has to hold when every other signal
// says collect.
func TestNothingCollectsTheCurrentBranchOrMain(t *testing.T) {
	for _, name := range []string{"main", "feat/here"} {
		if _, done := finished(name, "feat/here", "[gone]", true, true); done {
			t.Errorf("%q was collected while every other signal said finished; losing the branch you are on, or main, is not recoverable in the way the others are", name)
		}
	}
}
