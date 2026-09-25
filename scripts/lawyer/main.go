// Command lawyer holds the estate: the Cloudflare account and its Zero Trust
// organisation, which every site and every node stands on.
//
// Contractors build on a site; an estate is something held, bounded and
// granted access to, and establishing it is legal work done before any
// contractor arrives. So the estate has a program of its own, and the split
// is a credential boundary as much as a name. The lawyer holds the estate's
// credentials and only those: it renders config/estate.tpl.json from the
// one vault its token can see, and refuses to run if the token sees more. The
// contractor holds a site's credentials and only those. Neither program can
// change what the other owns, because neither is given the means to.
//
// What the estate owns is management/estate/, its own OpenTofu root with its
// own state in its own bucket. A site's demolish cannot reach any of it. The
// boundary between estate, site and node is in docs/epochs/02-abstraction.md.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"syscall"

	"homelab/details/console"
)

var knownVerbs = []string{"build-estate", "converge-estate", "demolish-estate"}

const usage = `lawyer holds the estate every site stands on.

usage: lawyer <verb> [flags]

verbs:
  build-estate     Establish an estate that does not exist yet. Refuses when
                   the estate's state already holds anything.
  converge-estate  Apply the estate's declarations to the estate that stands.
                   Refuses when there is no estate to converge.
  demolish-estate  Tear the estate down. Refuses while any site stands, and
                   requires -confirm.

Every verb renders the estate's secrets from the one vault its 1Password
token can see, and wipes them on the way out.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	verb := os.Args[1]
	if verb == "-h" || verb == "--help" || verb == "help" {
		fmt.Print(usage)
		return
	}
	if !slices.Contains(knownVerbs, verb) {
		fmt.Fprintf(os.Stderr, "unknown verb %q\n\n%s", verb, usage)
		os.Exit(2)
	}

	fs := flag.NewFlagSet(verb, flag.ExitOnError)
	confirm := false
	if verb == "demolish-estate" {
		fs.BoolVar(&confirm, "confirm", false, "required: this removes who may enroll a device, for every site")
	}
	_ = fs.Parse(os.Args[2:])
	if verb == "demolish-estate" && !confirm {
		fmt.Fprintln(os.Stderr, "error: demolish-estate removes the enrollment application, its policy and the split tunnel for the whole account. Pass -confirm.")
		os.Exit(2)
	}

	// Caught, not ignored: an ignored signal is inherited across exec, and
	// tofu must still receive Ctrl-C to release its lock and stop cleanly.
	// Catching it here only keeps this process alive until tofu has exited,
	// so the deferred sterilize still removes the rendered secrets.
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt, syscall.SIGTERM)
	go func() {
		for range interrupts {
			console.Warn("interrupted: waiting for tofu to stop, then removing the rendered secrets")
		}
	}()

	if err := execute(verb); err != nil {
		console.Fail(err.Error())
		os.Exit(1)
	}
	console.Ok(verb + " finished")
}
