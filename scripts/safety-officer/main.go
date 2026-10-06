// Command safety-officer says whether something may be destroyed.
//
// A teardown here is total, on purpose: it does not skip a volume or keep a
// disk, because a destructive act that is partial leaves things nothing
// tracks. That was the whole rule while nothing on a site was worth keeping.
// It is not enough once something is. So before anything is destroyed this is
// asked one question - does anything that should outlive it have its only
// good copy inside it? - and a teardown that would lose something does not
// start.
//
// It takes orders from nobody. There is no flag that skips it and none that
// lowers what it asks; the way past a refusal is to make the copy, or to stop
// the site holding the thing. It does not care which.
//
// It is a program of its own, and not a step of the contractor's, because
// the contractor is the party holding the detonator. What this holds is the
// means to look: a site's vault token, and from it a key to each bucket that
// can list and read and change nothing. It destroys nothing, saves nothing
// and fixes nothing.
//
// What is worth keeping, and for how long, is declared by whatever holds it
// (homelab/details/holds). This knows no asset by name.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"homelab/details/repopath"
)

const usage = `safety-officer says whether something may be destroyed.

usage: safety-officer clear -site <site> -destroying site

It reads what the site holds that is worth keeping, looks at the copy of
everything the destruction would take, and exits zero only when nothing that
should outlive it would be lost. It has no other flags and changes nothing.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, repopath.Root, time.Now()))
}

// run is the program: what it was asked, where to answer, how to find the
// repository whose declarations it reads, and what time it is. It returns
// how to exit: zero when cleared, one when refused, two when it was not
// asked a question it answers.
func run(args []string, out, complaints io.Writer, repository func() (string, error), now time.Time) int {
	if len(args) < 1 || args[0] != "clear" {
		fmt.Fprint(complaints, usage)
		return 2
	}
	flags := flag.NewFlagSet("clear", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	site := flags.String("site", "", "the site's key in the config")
	destroying := flags.String("destroying", "", "what is about to be destroyed: site")
	if err := flags.Parse(args[1:]); err != nil || *site == "" || *destroying == "" || flags.NArg() > 0 {
		fmt.Fprint(complaints, usage)
		return 2
	}
	root, err := repository()
	if err != nil {
		fmt.Fprintf(out, "REFUSED: the repository whose declarations say what %s holds was not found: %v\n", *site, err)
		return 1
	}
	if !clear(out, root, *site, *destroying, now) {
		return 1
	}
	return 0
}
