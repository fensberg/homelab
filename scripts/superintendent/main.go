// Command superintendent enforces how work proceeds on this site.
//
// On a construction site the superintendent runs the work itself: the
// sequence, the method, and whether a crew may proceed. They are not security -
// security checks what comes through the gate and walks the perimeter, which is
// a different question from whether the work in front of you is being done the
// agreed way.
//
// Three verbs, and all three refuse work that departs from the agreed process:
//
//	enforce-push            what may be published - a push that would produce
//	                        unsigned commits nobody can repair afterwards
//	enforce-merge           what cannot be published - a merge commit that
//	                        signedpush is unable to replay
//	enforce-standing-order  what the expediter may deliver under its standing
//	                        order - the game server's pin, and nothing else
//
// NOT "guard". Security guards: deliveries at the gate, egress leaving the
// estate, the perimeter. These three are not about who may come in or what may
// go out to whom - they are about whether this work follows the process
// everybody agreed to, which is the superintendent's job. The verbs say
// `enforce` for that reason, and the split is recorded in
// docs/epochs/03-workload.md.
//
// signedpush is deliberately NOT here, for the reason it was not in security
// either: enforce-push exists to refuse what signedpush does wrongly, and the
// party that raises a concern must never be the party that resolves it.
// signedpush also holds the App private key, and nothing that runs on every
// push should carry the code that reads it.
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
		{"enforce-push", "refuse a plain git push that would update a branch", enforcePush},
		{"enforce-merge", "refuse a merge commit signedpush could not publish", enforceMerge},
		{"enforce-standing-order", "refuse a delivery that changes more than the pin", enforceStandingOrder},
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
	fmt.Fprintf(os.Stderr, "superintendent: no such verb %q\n\n", os.Args[1])
	usage()
	os.Exit(2)
}

func usage() {
	fmt.Fprint(os.Stderr, "the superintendent enforces how work proceeds on this site.\n\nusage: superintendent <verb> [flags]\n\nverbs:\n")
	for _, v := range verbs() {
		fmt.Fprintf(os.Stderr, "  %-22s %s\n", v.name, v.what)
	}
	fmt.Fprintln(os.Stderr, "\nRun 'superintendent <verb> -h' for the flags a verb accepts.")
}
