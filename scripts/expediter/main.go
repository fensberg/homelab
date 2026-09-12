// Command expediter chases one order with one supplier: the Valheim dedicated
// server, published by Valve.
//
// On a construction site the expediter does not build or install anything. They
// find out whether the material is ready, and they get it to site when it is
// needed. These two verbs are those two questions, and they are separate
// because they cost wildly different amounts:
//
//	check        has Valve published anything? one HTTPS call, no key
//	release-due  is it the hour when taking delivery disturbs nobody?
//
// The heavy half - downloading two gigabytes of game files, building the image
// and pushing it - stays in .github/workflows/expediter.yml, because it is
// docker's job rather than this program's. What matters is that it only runs
// when `check` says there is something to collect, instead of every fifteen
// minutes on the chance that there is.
//
// WHY CHECK IS NOT AUTHORITATIVE. Steam has no cheap first-party way to ask for
// a dedicated server's build id: ISteamApps/UpToDateCheck refuses every
// dedicated-server appid (896660 here, and 740 for CS2), and the server appid's
// news feed carries press articles rather than Valve's own posts. What does
// work is the CLIENT appid's announcement feed, which is where Valve posts
// "Hotfix 1.0.10 & 1.0.12" - the patches that strand players.
//
// So this is a doorbell, not a receipt. It says "go and look properly", and the
// workflow then asks SteamCMD for the real build id and compares it with what
// is pinned. A missed announcement must therefore never be the only reason the
// estate believes it is current, which is why the workflow also takes an
// authoritative pass once a day whatever this says.
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
		{"check", "ask whether the supplier has published anything worth collecting", check},
		{"release-due", "say whether now is the hour a delivery may disturb the site", releaseDue},
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
	fmt.Fprintf(os.Stderr, "expediter: no such verb %q\n\n", os.Args[1])
	usage()
	os.Exit(2)
}

func usage() {
	fmt.Fprint(os.Stderr, "the expediter chases one order: the game server the estate runs.\n\nusage: expediter <verb> [flags]\n\nverbs:\n")
	for _, v := range verbs() {
		fmt.Fprintf(os.Stderr, "  %-13s %s\n", v.name, v.what)
	}
	fmt.Fprintln(os.Stderr, "\nRun 'expediter <verb> -h' for the flags a verb accepts.")
}
