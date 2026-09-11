// Command security is site security: what is checked in and out of the site,
// and the perimeter walked when nobody else is here.
//
// Security is a role rather than a place, and a role can hold more than one
// responsibility: everything arriving is checked against who is allowed to
// deliver, everything leaving is checked against how it is allowed to leave,
// and the estate is patrolled from outside. Five verbs across those three:
//
//	guard-deliveries  what comes in - third-party code installed as a commit hook
//	guard-standing-order  what the expediter may deliver - the game server's pin, nothing else
//	guard-push        what goes out - unsigned commits reaching a branch
//	guard-merge       what cannot go out - a merge commit signedpush cannot replay
//	patrol            the estate itself, watched from outside
//
// The verbs are verb phrases and the program is a noun, deliberately: security
// is who; guarding a delivery and walking a patrol are what they do. The list
// of who may deliver is scripts/approved-suppliers.yml - suppliers being the
// people, deliveries being what turns up at the gate.
//
// WHY THIS IS "security" AND NOT "gatehouse". It was security first, became the
// gatehouse while the only job was checking deliveries at the gate, and came
// back when that stopped fitting: a gatehouse is a building, and a building
// does not patrol. Site security does, and it also mans the gate - so the role
// that already did all four things gets the name that describes all four. The
// earlier rename is recorded in docs/epochs/01-ignition.md.
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
		{"guard-standing-order", "refuse an expediter pull request that changes more than the pin", guardStandingOrder},
		{"guard-push", "refuse a plain git push that would update a branch", guardPush},
		{"guard-merge", "refuse a merge commit that signedpush could not publish", guardMerge},
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
	fmt.Fprintf(os.Stderr, "security: no such verb %q\n\n", os.Args[1])
	usage()
	os.Exit(2)
}

func usage() {
	fmt.Fprint(os.Stderr, "site security checks what comes in and what goes out, and walks the perimeter.\n\nusage: security <verb> [flags]\n\nverbs:\n")
	for _, v := range verbs() {
		fmt.Fprintf(os.Stderr, "  %-16s %s\n", v.name, v.what)
	}
	fmt.Fprintln(os.Stderr, "\nRun 'security <verb> -h' for the flags a verb accepts.")
}
