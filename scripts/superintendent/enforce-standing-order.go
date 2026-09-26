package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"homelab/details/workorders"
)

// enforce-standing-order holds procurement's standing order to the one thing it
// covers: a new build of the game server from Valve, and nothing else.
//
// So that a Steam patch does not wait for a person - players' clients update
// the day Valve ships and refuse an older server - the procurement App may
// merge without review: it is a bypass actor on the ruleset that requires one.
// That permission is a standing order in the construction sense: approval
// given once, for one specified recurring item, and not for anything the holder
// feels like buying.
//
// Two kinds of pull request ride it, and each is held to exactly that item:
//
//   - The expediter's: it records Valve's new build as the VALHEIM_STEAM_BUILD_VERSION
//     line in scripts/versions.env and changes nothing else. The fabricator
//     builds the image from that pin.
//   - A delivery: procurement moving production's release pin in
//     clusters/management/releases.yaml. Under the bypass it passes only when
//     the release it brings differs from production's by the Steam build
//     alone - read from the source commits the two releases were built from,
//     which the registry records - so a release carrying anything somebody
//     wrote waits for a review however it arrived.
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
	pin := fs.String("pin", "scripts/versions.env", "the one file the expediter's pull request may change")
	orders := fs.String("orders", workorders.Path, "the work orders that say what each release is built from")
	repository := fs.String("repository", os.Getenv("GITHUB_REPOSITORY"), "owner/name, which names each release's package in the registry")
	releases := fs.String("releases", "clusters/management/releases.yaml", "the production releases file a delivery moves a pin in")
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

	// A delivery: procurement bringing a release the fabricator published to
	// the gate, as a pull request that moves one pin in the releases file.
	// Without the bypass it passes, so a person can merge it. Under the bypass
	// it passes only when the release changes nothing but the Steam build.
	if isDelivery(changedFiles, lines, *releases) {
		if !*bypass {
			fmt.Printf("%s delivered a release. The 4am window merges it if the Steam build is all that changed; otherwise it waits for a review\n", *author)
			return 0
		}
		j := judge{repository: *repository, releases: *releases, orders: *orders, pin: *pin, git: gitRunner}
		problems, err := j.steamOnly(*base, *head)
		if err != nil {
			fmt.Fprintln(os.Stderr, "superintendent enforce-standing-order: could not tell what this release changes, so it is not merged without review:", err)
			return 2
		}
		if len(problems) > 0 {
			fmt.Printf("REFUSED: %s delivered a release that changes more than the Steam build.\n\n", *author)
			for _, p := range problems {
				fmt.Println("  " + p)
			}
			fmt.Println("\nIt waits for a review.")
			return 1
		}
		fmt.Printf("%s delivered a release whose only change is the Steam build\n", *author)
		return 0
	}

	problems := withinStandingOrder(changedFiles, lines, *pin)
	if len(problems) > 0 {
		fmt.Printf("REFUSED: %s changed more than its standing order covers.\n\n", *author)
		for _, p := range problems {
			fmt.Println("  " + p)
		}
		fmt.Printf("\nThe order covers the VALHEIM_STEAM_BUILD_VERSION line in %s, and nothing else.\n"+
			"Anything more needs a review, and the expedite duty should never have been able to write it.\n", *pin)
		return 1
	}
	fmt.Printf("%s changed only the pin it holds a standing order for\n", *author)
	return 0
}

// steamBuildLine is the one line the expediter's pull request may change.
var steamBuildLine = regexp.MustCompile(`^[+-]VALHEIM_STEAM_BUILD_VERSION=[0-9]+$`)

// withinStandingOrder lists everything about the expediter's change that the
// order does not cover: exactly one Steam build replaced by another, in the
// pin file. An empty list is the only permitted answer.
func withinStandingOrder(files, changed []string, pin string) []string {
	if len(files) == 0 {
		return []string{"the pull request changes nothing, which is not a delivery"}
	}
	var problems []string
	for _, f := range files {
		if f != pin {
			problems = append(problems, "changes "+f)
		}
	}
	return append(problems, onlyTheSteamBuild(changed)...)
}

// onlyTheSteamBuild lists every changed line that is not the Steam build
// moving from one number to another.
func onlyTheSteamBuild(changed []string) []string {
	var problems []string
	removed, added := 0, 0
	for _, line := range changed {
		if !steamBuildLine.MatchString(line) {
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
		problems = append(problems, "does not replace exactly one Steam build with another")
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

// isDelivery reports whether a change moves release pins in the releases file
// and does nothing else: one file, and every changed line a release tag or a
// digest.
func isDelivery(files, changed []string, releases string) bool {
	if len(files) != 1 || files[0] != releases || len(changed) == 0 {
		return false
	}
	for _, line := range changed {
		if !deliveredTag.MatchString(line) && !deliveredDigest.MatchString(line) {
			return false
		}
	}
	return true
}
