// Command collector takes away what the work leaves behind.
//
// A construction site produces two kinds of leftover and they are not the same
// job. Something dangerous - a half-finished teardown, a machine nothing tracks
// - is the contractor's problem, because dealing with it can make things worse
// and it must be handled deliberately. Something merely finished is refuse, and
// refuse collection is boring on purpose: idempotent, safe to miss, and
// repaired by the next run doing exactly the same thing.
//
// docs/epochs/02-abstraction.md already drew that line for scheduling - refuse
// is the work "nobody observes the latency of", and a missed collection needs
// no retry because the next one collects the same thing. This program is that
// distinction made into a role. Nothing here may destroy something that is
// still in use, and nothing here is urgent.
//
//	clear-branches  local branches whose work has already landed
//
// WHY IT IS NOT PART OF SECURITY. Security decides what may come in
// and what may go out; it refuses things. This takes away things nobody
// refused. One role, one program - and a guard that also deletes is a guard
// somebody will be reluctant to run.
//
// WHAT IT DOES NOT COLLECT, established by looking rather than by guessing
// (2026-09-07): scratch signing refs (signedpush removes its own, and none had
// leaked), workflow artifacts and run logs (GitHub expires both). Published
// container image versions are the one thing that will need collecting next:
// one exists per image build, and nothing removes any of them.
//
// Remote branches were on that list, on the grounds that GitHub deletes a head
// branch on merge and only main existed. Both halves stopped being true: GitHub
// deletes on MERGE only, so a pull request closed without merging keeps its
// branch forever, and a workflow can push a branch and never open a pull
// request for it. By 2026-09-11 there were two of each kind - so clear-branches
// now takes finished remote branches too, but only when asked with -remote,
// because a remote branch is shared in a way a local one is not.
//
// It runs by itself after every pull (githooks/post-merge), which is the other
// thing the first version got wrong: see clear-branches.go.
package main

import (
	"fmt"
	"os"
)

type verb struct {
	name string
	what string
	run  func(args []string) int
}

func verbs() []verb {
	return []verb{
		{"clear-branches", "remove local branches whose work has already landed", clearBranches},
	}
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	for _, v := range verbs() {
		if v.name == os.Args[1] {
			os.Exit(v.run(os.Args[2:]))
		}
	}
	fmt.Fprintf(os.Stderr, "collector: no such verb %q\n\n", os.Args[1])
	usage()
	os.Exit(2)
}

func usage() {
	fmt.Fprint(os.Stderr, "the collector takes away what the work leaves behind.\n\nusage: collector <verb> [flags]\n\nverbs:\n")
	for _, v := range verbs() {
		fmt.Fprintf(os.Stderr, "  %-16s %s\n", v.name, v.what)
	}
	fmt.Fprintln(os.Stderr, "\nRun 'collector <verb> -h' for the flags a verb accepts.")
}
