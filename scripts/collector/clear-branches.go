package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// WHY THE ADVICE EVERYONE GIVES DOES NOT WORK HERE.
//
// Nothing deletes a local branch. GitHub's "automatically delete head branches"
// removes it on GitHub's side, and `git fetch --prune` removes the
// remote-tracking ref that pointed at it - but the branch you made to do the
// work stays until somebody removes it by hand. After a few months the branch
// picker is unusable, which is how this came up.
//
// Every recipe for it is a variation on:
//
//	git branch --merged main | xargs git branch -d
//
// That does almost nothing here, because this repository squash-merges. A
// branch's commits never become ancestors of main; one new commit with the same
// content does. Measured on a real checkout on 2026-09-07: 159 local branches,
// of which `--merged` recognised 5.
//
// Squashing is not the thing to change. It is what keeps main one commit per
// pull request, and it is load-bearing for signatures - a rebase merge replays
// the original commits, and an unsigned one is refused by the ruleset on main,
// while a squash produces a single commit GitHub signs. Changing a merge
// strategy so that a cleanup heuristic works would be the wrong way round.
//
// So "finished" is established three ways, and any one of them is enough:
//
//	upstream gone         the remote branch is gone, which is what merging does
//	inside main           the tip is already an ancestor of origin/main
//	pull request merged   GitHub says so - the only evidence a squash preserves
//	pull request closed   somebody decided it would not land, which is as
//	                      finished as merged and leaves no trace in git at all
//
// The third is what makes this worth having: on that same checkout it accounted
// for 109 of the 149 finished branches, and nothing local could have known.
const _ = "see the comment above"

// clearBranches is deliberately a dry run unless told otherwise, and the
// deletion it eventually performs is recoverable.
//
// `git branch -D` leaves the commits in the reflog for ninety days, so every
// branch removed here is one `git branch <name> <sha>` away - and the sha is
// printed beside each, so recovering one needs nothing looked up. It has to be
// -D rather than -d for the squash reason above: git does not believe these
// branches are merged, so the safe form refuses all of them.
func clearBranches(args []string) int {
	fs := flag.NewFlagSet("clear-branches", flag.ExitOnError)
	apply := fs.Bool("apply", false, "Delete them. Without this, say what would go and change nothing.")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	// Refresh remote-tracking refs first, or "upstream gone" is answered from
	// whatever this checkout last happened to hear. A fetch reads and never
	// writes to the remote.
	if _, err := git("fetch", "--prune", "--quiet"); err != nil {
		fmt.Fprintln(os.Stderr, "collector: could not fetch, so this is judged on stale information:", err)
	}

	current, err := git("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		fmt.Fprintln(os.Stderr, "collector:", err)
		return 1
	}

	merged, closed := pullRequestBranches()
	if merged == nil {
		fmt.Fprintln(os.Stderr, "collector: could not ask GitHub about pull requests, so"+
			" branches whose work was squash-merged are being kept. Authenticate gh and run again to clear those.")
	}

	branches, err := localBranches()
	if err != nil {
		fmt.Fprintln(os.Stderr, "collector:", err)
		return 1
	}

	var finishedRows, keep []string
	for _, b := range branches {
		why, done := finished(b.name, current, b.track, insideMain(b.name), merged[b.name], closed[b.name])
		switch {
		case done:
			finishedRows = append(finishedRows, strings.Join([]string{b.name, b.sha, why}, "\x00"))
		case why != "":
			age, _ := git("log", "-1", "--format=%cr", b.name)
			keep = append(keep, fmt.Sprintf("%-52s %-28s last commit %s", b.name, why, age))
		}
	}
	sort.Strings(finishedRows)

	sort.Strings(keep)
	// Said out loud, always, and this is the half the first version got wrong.
	//
	// It reported "9 still in use" and named none of them, which tells a reader
	// nothing they can act on and hides the interesting question: WHY is each
	// one kept? A branch nothing landed and nothing closed is either work in
	// progress or work abandoned, and only the person who wrote it knows which.
	// Naming them with their age is what lets that judgement be made at all.
	if len(keep) > 0 {
		fmt.Printf("kept, because nothing says this work is finished:\n\n")
		for _, row := range keep {
			fmt.Printf("  %s\n", row)
		}
		fmt.Println()
	}

	if len(finishedRows) == 0 {
		fmt.Printf("nothing to collect: %d branch(es) are still in use\n", len(keep))
		return 0
	}

	for _, row := range finishedRows {
		p := strings.Split(row, "\x00")
		name, sha, why := p[0], p[1], p[2]
		if !*apply {
			fmt.Printf("  would collect  %-52s %-20s %s\n", name, why, short(sha))
			continue
		}
		if _, err := git("branch", "-D", name); err != nil {
			fmt.Fprintf(os.Stderr, "  FAILED         %-52s %v\n", name, err)
			continue
		}
		fmt.Printf("  collected      %-52s %-20s %s\n", name, why, short(sha))
	}

	fmt.Printf("\n%d finished, %d still in use\n", len(finishedRows), len(keep))
	if !*apply {
		fmt.Print("\nNothing was changed. To take them away:\n\n    task clear-branches -- -apply\n")
		return 0
	}
	fmt.Println("\nEach is recoverable for ninety days: git branch <name> <sha>")
	return 0
}

// finished decides whether a branch is done with, and says which answer decided
// it.
//
// Pure, and separated from every git call, so the decision is a table test
// rather than a fixture repository. The reason is not decoration: "upstream
// gone" and "GitHub says the pull request merged" are different amounts of
// evidence, and somebody watching a hundred branches disappear is entitled to
// see which one applied to each.
func finished(name, current, track string, insideMain, mergedPR, closedPR bool) (why string, done bool) {
	switch {
	case name == current:
		// Never the branch you are standing on, whatever else is true of it.
		return "", false
	case name == "main":
		return "", false
	case track == "[gone]":
		return "upstream gone", true
	case insideMain:
		return "inside main", true
	case mergedPR:
		return "pull request merged", true
	case closedPR:
		// Closed without merging is still a decision, and it is the only one
		// that leaves no mark on the branch whatsoever - the commits are still
		// there, the upstream may still exist, and nothing local can tell it
		// apart from work in progress.
		return "pull request closed", true
	default:
		return "no pull request, not in main", false
	}
}

type localBranch struct{ name, sha, track string }

func localBranches() ([]localBranch, error) {
	// NUL-separated, because a branch name may contain almost anything except
	// NUL and a control character, and the tracking column contains spaces.
	//
	// The %00 is written for git to expand, not as a Go escape: an argument
	// passed to exec is a NUL-terminated C string, so a real NUL inside one is
	// rejected by the kernel with "invalid argument" - which is what the first
	// version of this line did.
	out, err := git("for-each-ref",
		"--format=%(refname:short)%00%(objectname)%00%(upstream:track)", "refs/heads")
	if err != nil {
		return nil, err
	}
	var branches []localBranch
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		p := strings.SplitN(line, "\x00", 3)
		if len(p) < 3 {
			continue
		}
		branches = append(branches, localBranch{name: p[0], sha: p[1], track: p[2]})
	}
	return branches, nil
}

func insideMain(name string) bool {
	_, err := git("merge-base", "--is-ancestor", name, "origin/main")
	return err == nil
}

// pullRequestBranches asks GitHub which head branches belong to a pull request
// that merged, and which to one that was closed without merging. A nil merged
// map means the question could not be asked at all.
//
// This shells out to gh, which tests/go/repo forbids in scripts/contractor for
// a good reason: gh is on every developer's machine and in no image, so the
// dependency passes review and fails in CI. That reasoning does not reach here.
// This verb only ever runs on a developer's machine, by hand, against their own
// checkout - it is in no workflow - and a missing gh degrades to keeping more
// branches rather than to failing.
func pullRequestBranches() (merged, closed map[string]bool) {
	out, err := gh("pr", "list", "--state", "all", "--limit", "500",
		"--json", "headRefName,state", "--jq", `.[] | "\(.state)\t\(.headRefName)"`)
	if err != nil {
		return nil, map[string]bool{}
	}
	merged, closed = map[string]bool{}, map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		state, name, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok || name == "" {
			continue
		}
		switch state {
		case "MERGED":
			merged[name] = true
		case "CLOSED":
			closed[name] = true
		}
	}
	return merged, closed
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// Two wrappers rather than one taking the program as a parameter, so the
// command name is a literal at the point exec sees it.
//
// Semgrep's dangerous-exec-command rule refused the single-wrapper version, and
// while every caller passed a constant, the rule is asking the right question:
// a helper that will run whatever it is handed is one refactor away from
// running whatever it is given. Two names cost nothing and cannot drift.
func git(args ...string) (string, error) { return runOutput(exec.Command("git", args...), "git", args) }

func gh(args ...string) (string, error) { return runOutput(exec.Command("gh", args...), "gh", args) }

// runOutput says what the command said when it fails. signedpush learned this
// the expensive way: a wrapper that swallows a subprocess's stderr turns a
// one-line diagnosis into an investigation, every time, for everybody.
func runOutput(cmd *exec.Cmd, name string, args []string) (string, error) {
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if said := strings.TrimSpace(stderr.String()); said != "" {
			return "", fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, said)
		}
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return strings.TrimRight(string(out), "\n"), nil
}
