package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path"
	"regexp"
	"sort"
	"strings"

	"homelab/details/applications"
	"homelab/details/pins"
	"homelab/details/workorders"
)

// enforce-standing-order holds procurement's standing order to the one thing it
// covers: a new build from the supplier an application declares, and nothing
// else.
//
// So that a supplier's patch does not wait for a person - players' clients
// update the day it ships and refuse an older server - the procurement App may
// merge without review: it is a bypass actor on the ruleset that requires one.
// That permission is a standing order in the construction sense: approval
// given once, for one specified recurring item, and not for anything the holder
// feels like buying.
//
// Two kinds of pull request ride it, and each is held to exactly that item:
//
//   - The expediter's: it records the supplier's new build on one line of one
//     application's own pins file - the line that application declares as
//     its upstream's pin - and changes nothing else. The fabricator builds
//     the image from that pin.
//   - A delivery: procurement moving an application's release pin in the
//     sites that run it. Under the bypass it passes only when the release it
//     brings differs from the one each site runs by that build alone - read
//     from the source commits the two releases were built from, which the
//     registry records - so a release carrying anything somebody wrote waits
//     for a review however it arrived.
//   - A pin move: procurement moving the estate's default pin to a commit
//     that changed what a site runs. It passes on the pull request, so a
//     person can merge it, and never under the bypass: which version of the
//     modules every site runs is nobody's to change without a review.
//
// Which line that is, is the application's to declare and never this
// program's to know: it reads every application's declaration as it stood at
// the pull request's base, where the pull request could not have written it.
//
// It runs twice: in the Sensitive Paths check, so the verdict is visible on the
// pull request, and in expedite.yml immediately before the merge, against the
// exact head commit merged. The second is the one that binds, because the
// review rule and the required checks share one ruleset and a bypass actor
// skips both.
//
// The holder's login comes from the PROCUREMENT_BOT_LOGIN repository variable, so
// no name of this estate is written here. With it unset there is no standing
// order: every pull request is judged by review as usual, and the expedite
// workflow refuses to merge anything - so the bypass is never used with this
// check unarmed.
func enforceStandingOrder(args []string) int {
	fs := flag.NewFlagSet("enforce-standing-order", flag.ExitOnError)
	author := fs.String("author", os.Getenv("PR_AUTHOR"), "the pull request's author login")
	holder := fs.String("holder", os.Getenv("PROCUREMENT_BOT_LOGIN"), "the login the standing order was given to")
	base := fs.String("base", "", "the pull request's base commit")
	head := fs.String("head", "", "the pull request's head commit")
	pins := fs.String("pins", "scripts/versions.env", "the estate's own pins, which a release merged without review may not differ in")
	orders := fs.String("orders", workorders.Path, "the estate's own work orders, which such a release may not differ in either")
	repository := fs.String("repository", os.Getenv("GITHUB_REPOSITORY"), "owner/name, which names each release's package in the registry")
	releases := fs.String("releases", applications.SitesDir+"/*/"+applications.SiteFile, "which files say what a site runs: a delivery moves pins in those and nothing else")
	bypass := fs.Bool("bypass", false, "judge for a merge without review: only what the order covers passes, and a delivery does not")
	_ = fs.Parse(args)

	switch {
	case *holder == "":
		fmt.Println("no standing order is configured (PROCUREMENT_BOT_LOGIN is unset), so nothing is merged under one")
		return 0
	case *author != *holder:
		fmt.Printf("%s holds no standing order; this pull request is reviewed as usual\n", *author)
		return 0
	case *base == "" || *head == "":
		fmt.Fprintln(os.Stderr, "superintendent enforce-standing-order: -base and -head are required to judge a pull request under the order")
		return 2
	}

	files, err := exec.Command("git", "diff", "--name-only", *base, *head).Output()
	if err != nil {
		fmt.Fprintln(os.Stderr, "superintendent enforce-standing-order: could not diff the pull request:", err)
		return 2
	}
	diff, err := exec.Command("git", "diff", "--unified=0", *base, *head).Output()
	if err != nil {
		fmt.Fprintln(os.Stderr, "superintendent enforce-standing-order: could not diff the pull request:", err)
		return 2
	}

	changedFiles, lines := nonEmptyLines(string(files)), changedLines(string(diff))

	j := judge{repository: *repository, orders: *orders, pins: *pins, git: gitRunner}

	// A delivery: procurement bringing a release the fabricator published to
	// the gate, as a pull request that moves an application's pin in the
	// sites that run it. Without the bypass it passes, so a person can merge
	// it. Under the bypass it passes only when the release changes nothing
	// but the build the application's supplier published.
	if isDelivery(changedFiles, lines, *releases) {
		if !*bypass {
			fmt.Printf("%s delivered a release. The 4am window merges it if the supplier's build is all that changed; otherwise it waits for a review\n", *author)
			return 0
		}
		// Every file the delivery changed, each of which isDelivery has just
		// held to the pattern: whichever sites' they are.
		var problems []string
		for _, site := range changedFiles {
			found, err := j.upstreamOnly(site, *base, *head)
			if err != nil {
				fmt.Fprintln(os.Stderr, "superintendent enforce-standing-order: could not tell what this release changes, so it is not merged without review:", err)
				return 2
			}
			problems = append(problems, found...)
		}
		if len(problems) > 0 {
			fmt.Printf("REFUSED: %s delivered a release that changes more than the supplier's build.\n\n", *author)
			for _, p := range problems {
				fmt.Println("  " + p)
			}
			fmt.Println("\nIt waits for a review.")
			return 1
		}
		fmt.Printf("%s delivered a release whose only change is the supplier's build\n", *author)
		return 0
	}

	// A pin move: procurement proposing that the sites run the modules as
	// they now are. A person's to merge, always.
	if isPinMove(changedFiles, lines) {
		if *bypass {
			fmt.Printf("REFUSED: %s moved the estate's default pin. Which modules every site runs is never changed without a review.\n", *author)
			return 1
		}
		fmt.Printf("%s proposed moving the estate's default pin. It is merged by a person, after its plan has been read\n", *author)
		return 0
	}

	// Each application's declaration as the base has it: the pull request
	// cannot have written what it is judged by.
	declared, err := j.declaredAt(*base)
	if err != nil {
		fmt.Fprintln(os.Stderr, "superintendent enforce-standing-order: could not read what the applications declare, so nothing is merged without review:", err)
		return 2
	}
	problems := withinStandingOrder(changedFiles, lines, declared)
	if len(problems) > 0 {
		fmt.Printf("REFUSED: %s changed more than its standing order covers.\n\n", *author)
		for _, p := range problems {
			fmt.Println("  " + p)
		}
		fmt.Println("\nThe order covers the one line an application declares as its upstream's pin, in that application's own pins file, and nothing else.\n" +
			"Anything more needs a review, and the expedite duty should never have been able to write it.")
		return 1
	}
	fmt.Printf("%s changed only the pin it holds a standing order for\n", *author)
	return 0
}

// pinLine is the one line of an application's pins file the order covers:
// the pin its declaration names, set to a number.
func pinLine(pin string) *regexp.Regexp {
	return regexp.MustCompile(`^[+-]` + regexp.QuoteMeta(pin) + `=[0-9]+$`)
}

// withinStandingOrder lists everything about the expediter's change that the
// order does not cover: exactly one build replaced by another, on the line
// one application declares as its upstream's pin, in that application's own
// pins file. An empty list is the only permitted answer.
func withinStandingOrder(files, changed []string, declared map[string]applications.Application) []string {
	if len(files) == 0 {
		return []string{"the pull request changes nothing, which is not a delivery"}
	}
	// The application whose pins file this is, if it is one's and that
	// application declares an upstream.
	var covered *applications.Application
	for _, f := range files {
		for _, name := range sortedApplications(declared) {
			if a := declared[name]; a.Upstream != nil && a.PinsFile() == f && covered == nil {
				covered = &a
			}
		}
	}
	var problems []string
	for _, f := range files {
		if covered == nil || f != covered.PinsFile() {
			problems = append(problems, "changes "+f)
		}
	}
	if covered == nil {
		return append(problems, "changes no pins file of an application that declares an upstream")
	}
	return append(problems, onlyThePin(changed, covered.Upstream.Pin)...)
}

// onlyThePin lists every changed line that is not the named pin moving from
// one number to another.
func onlyThePin(changed []string, pin string) []string {
	covered := pinLine(pin)
	var problems []string
	removed, added := 0, 0
	for _, line := range changed {
		if !covered.MatchString(line) {
			problems = append(problems, "changes a line the order does not cover: "+strings.TrimSpace(line))
			continue
		}
		if strings.HasPrefix(line, "-") {
			removed++
		} else {
			added++
		}
	}
	if len(problems) == 0 && (removed != 1 || added != 1) {
		problems = append(problems, "does not replace exactly one build with another")
	}
	return problems
}

// changedLines keeps the added and removed lines of a unified diff, dropping
// file headers, hunk headers and context.
func changedLines(diff string) []string {
	var out []string
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") {
			continue
		}
		if strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") {
			out = append(out, line)
		}
	}
	return out
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

var (
	deliveredTag    = regexp.MustCompile(`^[+-]\s+tag:\s+"[0-9][0-9A-Za-z.]*-[0-9]+"\s*$`)
	deliveredDigest = regexp.MustCompile(`^[+-]\s+digest:\s+"sha256:[0-9a-f]{64}"\s*$`)
)

// isDelivery reports whether a change moves release pins in sites' files and
// does nothing else: every file one that says what a site runs, by the
// pattern that says which those are, and every changed line a release tag or
// a digest. A pattern rather than a path, because which sites were given the
// work is the Flux tree's to say and not this program's to name; and more
// than one file, because one release goes to every site that follows it.
func isDelivery(files, changed []string, releases string) bool {
	if len(files) == 0 || len(changed) == 0 {
		return false
	}
	for _, f := range files {
		if isReleases, err := path.Match(releases, f); err != nil || !isReleases {
			return false
		}
	}
	for _, line := range changed {
		if !deliveredTag.MatchString(line) && !deliveredDigest.MatchString(line) {
			return false
		}
	}
	return true
}

func sortedApplications(m map[string]applications.Application) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// movedDefault is the one line of the pins file a pin move changes: the
// default, set to a full commit hash.
var movedDefault = regexp.MustCompile(`^[+-]\s*"default":\s*"[0-9a-f]{40}",?\s*$`)

// isPinMove reports whether a change moves the estate's default pin and does
// nothing else: one file, the pins, and one line of it replaced by another.
// A site's own pin is not the default, and is not covered.
func isPinMove(files, changed []string) bool {
	if len(files) != 1 || files[0] != pins.File || len(changed) != 2 {
		return false
	}
	removed, added := 0, 0
	for _, line := range changed {
		if !movedDefault.MatchString(line) {
			return false
		}
		if strings.HasPrefix(line, "-") {
			removed++
		} else {
			added++
		}
	}
	return removed == 1 && added == 1
}
