// Command gatehouse is where things are checked in and out of the site.
//
// A gatehouse is a place rather than a task, which is why it holds more than
// one: everything arriving is checked against who is allowed to deliver,
// everything leaving is checked against how it is allowed to leave, and the
// perimeter is walked when nobody else is here. Three verbs, one for each:
//
//	guard-deliveries  what comes in - third-party code installed as a commit hook
//	guard-push        what goes out - unsigned commits reaching a branch
//	patrol            the estate itself, watched from outside
//
// The verbs are verb phrases and the program is a noun, deliberately. A
// gatehouse is somewhere; guarding a delivery is something you do there. The
// list of who may deliver is scripts/approved-suppliers.yml - suppliers being
// the people, deliveries being what turns up at the gate.
//
// One program rather than three because they are one role, and a role can have
// several responsibilities. Three modules meant three entries in the taskfile,
// three build artifacts to ignore, and three chances to forget one - which is
// how a binary reached a commit.
//
// signedpush is deliberately NOT here. It is what publishes; guard-push exists
// to refuse what it does. Putting the guard and the thing it constrains in one
// binary is the shape this estate refuses everywhere else - the party that
// raises a concern must never be the party that resolves it. signedpush also
// holds the App private key, and nothing that runs on every commit should carry
// the code that reads it.
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
		{"guard-deliveries", "refuse a delivery from an unapproved supplier", guardDeliveries},
		{"guard-push", "refuse a plain git push that would update a branch", guardPush},
		{"patrol", "check from outside that the estate is still answering", patrol},
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
	fmt.Fprintf(os.Stderr, "gatehouse: no such verb %q\n\n", os.Args[1])
	usage()
	os.Exit(2)
}

func usage() {
	fmt.Fprint(os.Stderr, "the gatehouse checks what comes in and what goes out.\n\nusage: gatehouse <verb> [flags]\n\nverbs:\n")
	for _, v := range verbs() {
		fmt.Fprintf(os.Stderr, "  %-16s %s\n", v.name, v.what)
	}
	fmt.Fprintln(os.Stderr, "\nRun 'gatehouse <verb> -h' for the flags a verb accepts.")
}
