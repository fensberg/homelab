package main

import (
	"flag"
	"fmt"
	"os"

	"homelab/details/platform"
)

// deliverPlatform brings a release of the platform to the estate's gate.
//
// The fabricator publishes a release of the platform and deploys nothing: a
// site runs the release its own line names. Moving that line was done by
// hand, each time, by whoever remembered that a release had been published
// - several times in one day when a site was being given something new, each
// one a pull request somebody had to be asked for. This is the same
// delivery an application's release already gets: the line is moved in the
// file, and the workflow around it opens the pull request.
//
// Every site's line moves. A site that should stay behind has no way to say
// so yet, and with one site none is needed; the day there are two, the
// first of them to want it is where that is decided.
//
// Text in and text out, like deliver, and it changes only the lines that
// say a release.
func deliverPlatform(args []string) int {
	fs := flag.NewFlagSet("deliver-platform", flag.ContinueOnError)
	file := fs.String("pins", "", "the file each site's release of the platform is named in")
	version := fs.String("version", "", "the release's version, e.g. v2026.10.22")
	digest := fs.String("digest", "", "the release's digest, sha256:<64 hex>")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" || *version == "" || *digest == "" {
		fmt.Fprintln(os.Stderr, "procurement deliver-platform: -pins, -version and -digest are all required")
		return 2
	}
	body, err := os.ReadFile(*file)
	if err != nil {
		fmt.Fprintln(os.Stderr, "procurement deliver-platform:", err)
		return 1
	}
	moved, sites, err := platform.MovePins(string(body), platform.Pin{Version: *version, Digest: *digest})
	if err != nil {
		fmt.Fprintln(os.Stderr, "procurement deliver-platform:", err)
		return 1
	}
	if sites == 0 {
		fmt.Printf("every site already runs %s\n", *version)
		return 0
	}
	if err := os.WriteFile(*file, []byte(moved), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "procurement deliver-platform:", err)
		return 1
	}
	fmt.Printf("moved %d site(s) to %s\n", sites, *version)
	return 0
}
