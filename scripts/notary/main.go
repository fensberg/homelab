// Command notary issues each site the certificate it serves under.
//
// A certificate says that whoever serves it was given it by somebody who
// controls the name. Here that is the operator and nobody else, and being
// issued one is what a subscription is: a site whose certificate is not
// renewed stops being served to anything that checks
// (docs/epochs/11-black-box-white-glove.md). So issuing is a party of its
// own, with a program of its own, and not a step of building a site: no site
// holds the means to be issued one, and no site's program is given them.
//
// What it holds is the operator's: the domain every name is under, the
// credential that changes its records, and the key the issuer knows the
// operator by. They are kept in a vault of the operator's own, which no
// estate's token and no site's sees, and the notary's token sees nothing of
// an estate's. An estate says what a site serves; only here is that given a
// name.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"homelab/details/console"
)

const usage = `notary issues each site the certificate it serves under.

usage: notary issue [-rehearse]

  issue  For each site the estate has granted routes to: make a name for
         each route that resolves to it, and issue a certificate for exactly
         those names when the site holds none, holds one for other names, or
         holds one with less than twenty days left. Write it into the site's
         -shared vault. A site whose certificate is current is issued nothing.

  -rehearse  Ask the issuer's rehearsal service, whose certificates no
             browser trusts and whose limits are generous. The names are
             made as they would be; no certificate is granted and the
             domain is not bound to an account.

Runs on a 1Password service account of its own, which sees the operator's
vault and each site's -shared vault and nothing of the estate's, after the
lawyer has granted each site what it serves.
`

func main() {
	status := run(os.Args[1:])
	if status != 0 {
		os.Exit(status)
	}
}

// run is the program with its arguments, and the status it ends with: 2 for
// something it was not asked properly, 1 for something it could not do.
func run(args []string) int {
	flags := flag.NewFlagSet("issue", flag.ContinueOnError)
	flags.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	rehearse := flags.Bool("rehearse", false, "ask the issuer's rehearsal service and write nothing")
	if len(args) < 1 || args[0] != "issue" || flags.Parse(args[1:]) != nil || flags.NArg() > 0 {
		flags.Usage()
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := issue(ctx, *rehearse); err != nil {
		console.Fail(err.Error())
		return 1
	}
	return 0
}
