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
		closedPR   bool
		openPR     bool
		wantWhy    string
		wantDone   bool
	}{
		{
			name:   "the branch you are standing on is never collected",
			branch: "feat/in-progress", current: "feat/in-progress",
			track: "[gone]", insideMain: true, mergedPR: true, closedPR: true,
			wantWhy: "", wantDone: false,
		},
		{
			name:   "main is never collected",
			branch: "main", current: "feat/other",
			track: "[gone]", insideMain: true, mergedPR: true, closedPR: true,
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
			// Closed without merging leaves no mark in git at all: the commits
			// are still there and the upstream may still exist. Only GitHub
			// knows somebody decided against it.
			name:   "a pull request closed without merging is a decision too",
			branch: "spike/rejected", current: "main",
			track: "", insideMain: false, closedPR: true,
			wantWhy: "pull request closed", wantDone: true,
		},
		{
			name:   "work that landed nowhere is kept",
			branch: "spike/abandoned", current: "main",
			track: "", insideMain: false, mergedPR: false,
			wantWhy: "no pull request, not in main", wantDone: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			why, done := finished(tc.branch, tc.current, tc.track, tc.insideMain, tc.mergedPR, tc.closedPR, tc.openPR)
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
		if _, done := finished(name, "feat/here", "[gone]", true, true, true, false); done {
			t.Errorf("%q was collected while every other signal said finished; losing the branch you are on, or main, is not recoverable in the way the others are", name)
		}
	}
}

// A branch name can carry more than one pull request, and an open one means the
// work is not finished whatever an older one says.
//
// Squash-merged repositories reuse head branch names routinely - a follow-up is
// opened from the same branch after the first merged. The collector used to ask
// "did a pull request for this branch merge", find the old one, and take away a
// branch with work in progress on it.
func TestAnOpenPullRequestKeepsTheBranch(t *testing.T) {
	why, done := finished("feat/reused", "main", "", true, true, false, true)
	if done {
		t.Fatalf("collected a branch with an open pull request (said %q) - that is work in progress", why)
	}
	if why != "pull request open" {
		t.Errorf("kept it, but said %q; the reason printed is what lets a person trust the list", why)
	}
}

// Every page, not the first five hundred.
//
// The query was `gh pr list --limit 500`. At about 380 pull requests that
// covered everything, and at the pace this repository opens them it would have
// stopped covering everything within weeks - after which every branch whose
// pull request had fallen off the end would be kept as "no pull request",
// silently, while the command still reported success.
func TestPullRequestsAreReadWithTheirRealState(t *testing.T) {
	merged, closed, open := parsePullRequests(
		"MERGED\tfeat/a\n" +
			"CLOSED\tfeat/b\n" +
			"OPEN\tfeat/c\n" +
			"MERGED\tfeat/reused\n" +
			"OPEN\tfeat/reused\n" +
			"\n" +
			"garbage without a tab\n")
	if !merged["feat/a"] || !closed["feat/b"] || !open["feat/c"] {
		t.Errorf("states lost: merged=%v closed=%v open=%v", merged, closed, open)
	}
	if !merged["feat/reused"] || !open["feat/reused"] {
		t.Errorf("a reused branch name must be recorded under every state it has, got merged=%v open=%v", merged["feat/reused"], open["feat/reused"])
	}
	if len(merged)+len(closed)+len(open) != 5 {
		t.Errorf("read %d entries, want 5 - blank and malformed lines must be skipped", len(merged)+len(closed)+len(open))
	}
}

// Remote branches are shared, so the rule for them is narrower than for local
// ones: only a branch whose pull request is finished may go, never one with an
// open pull request, never main or an epoch branch, and never one with no pull
// request at all - that is a branch something pushed and nobody explained, and
// taking it away would destroy the only evidence of why.
func TestOnlyAFinishedRemoteBranchMayGo(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		merged, closed, open bool
		wantDone             bool
		wantWhy              string
	}{
		{name: "main", merged: true, wantDone: false, wantWhy: ""},
		{name: "epoch/03-workload", merged: true, wantDone: false, wantWhy: ""},
		{name: "feat/open", merged: true, open: true, wantDone: false, wantWhy: "pull request open"},
		{name: "revert/converge-failed-abcdef12", wantDone: false, wantWhy: "no pull request"},
		{name: "feat/closed", closed: true, wantDone: true, wantWhy: "pull request closed"},
		{name: "feat/merged", merged: true, wantDone: true, wantWhy: "pull request merged"},
	} {
		why, done := finishedRemote(tc.name, tc.merged, tc.closed, tc.open)
		if done != tc.wantDone || why != tc.wantWhy {
			t.Errorf("%s: got (%q, %v), want (%q, %v)", tc.name, why, done, tc.wantWhy, tc.wantDone)
		}
	}
}
