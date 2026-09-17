// Command procurement deals with deliveries: deciding what this estate takes
// from its suppliers, recording it, and chasing the one order that cannot wait.
//
// On a construction site procurement does not build or install anything. It
// selects and orders material, records exactly what was ordered, and - when
// something is holding the site up - expedites it. Security checks what
// arrives against that record at the gate; procurement never checks its own
// work.
//
//	order                  write scripts/deliveries.lock from the declared
//	                       versions: every delivery pinned by version and hash
//	expedite-check         has Valve published anything? one HTTPS call, no key
//	expedite-release-due   is it the hour when taking delivery disturbs nobody?
//
// THE BYPASS BELONGS TO THE DUTY, NOT THE ROLE. Expediting is the one duty here
// that holds elevated permission: its GitHub App may merge its own delivery
// without a review, bounded by `superintendent enforce-standing-order`. That
// permission lives in .github/workflows/expedite.yml and its credential, and
// the expedite-* prefix is what names the duty in every verb that serves it.
// Ordering holds no permission at all - it writes a file somebody commits
// through an ordinary review - and tests/go/repo refuses any workflow that runs
// `order`, and any expedite workflow that runs a verb outside its duty. More
// duties widen the role's scope, never its power (#416).
//
// EXPEDITING THE GAME SERVER. The heavy half - downloading two gigabytes of
// game files, building the image and pushing it - stays in the workflow,
// because it is docker's job rather than this program's. What matters is that
// it only runs when expedite-check says there is something to collect, instead
// of every fifteen minutes on the chance that there is.
//
// WHY EXPEDITE-CHECK IS NOT AUTHORITATIVE. Steam has no cheap first-party way
// to ask for a dedicated server's build id: ISteamApps/UpToDateCheck refuses
// every dedicated-server appid (896660 here, and 740 for CS2), and the server
// appid's news feed carries press articles rather than Valve's own posts. What
// does work is the CLIENT appid's announcement feed, which is where Valve posts
// "Hotfix 1.0.10 & 1.0.12" - the patches that strand players.
//
// So this is a doorbell, not a receipt. It says "go and look properly", and the
// workflow then asks SteamCMD for the real build id and compares it with what
// is pinned. A missed announcement must therefore never be the only reason the
// estate believes it is current, which is why the workflow also takes an
// authoritative pass once a day whatever this says.
//
// This program was the expediter until #416, when ordering joined it and the
// role got the name that covers both.
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
		{"order", "write scripts/deliveries.lock: every delivery pinned by version and hash", order},
		{"expedite-check", "ask whether the game server's supplier has published anything worth collecting", expediteCheck},
		{"expedite-release-due", "say whether now is the hour an expedited delivery may disturb the site", expediteReleaseDue},
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
	fmt.Fprintf(os.Stderr, "procurement: no such verb %q\n\n", os.Args[1])
	usage()
	os.Exit(2)
}

func usage() {
	fmt.Fprint(os.Stderr, "procurement orders what the estate takes delivery of, and expedites the game server.\n\nusage: procurement <verb> [flags]\n\nverbs:\n")
	for _, v := range verbs() {
		fmt.Fprintf(os.Stderr, "  %-21s %s\n", v.name, v.what)
	}
	fmt.Fprintln(os.Stderr, "\nRun 'procurement <verb> -h' for the flags a verb accepts.")
}
