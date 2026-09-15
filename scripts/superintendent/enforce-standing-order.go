package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// enforce-standing-order holds the expediter to the one thing it may change.
//
// The expediter watches Valve for a new build of the game server, builds it,
// and opens a pull request pinning the new digest. So that a Steam patch does
// not wait for a person, it may be allowed to merge that pull request without a
// review - it is a bypass actor on the ruleset that requires one. That
// permission is a standing order in the construction sense: approval given once,
// for one specified recurring item, and not for anything the holder feels like
// buying.
//
// This is what makes it that narrow. It runs in a required check the expediter
// cannot skip - bypassing the review ruleset does not bypass the one requiring
// checks - and refuses any pull request authored by the expediter that changes
// anything except the game server's image digest and the Steam build recorded
// beside it. It also refuses a digest from a different image repository, which
// is the change that would read exactly like a routine update in a diff.
//
// The expediter's login comes from the EXPEDITER_LOGIN repository variable, so
// no name of this estate is written here. With it unset there is no standing
// order: every pull request is judged by review as usual, and the expediter's
// workflow refuses to merge anything - so the bypass is never used with this
// check unarmed.
func enforceStandingOrder(args []string) int {
	fs := flag.NewFlagSet("enforce-standing-order", flag.ExitOnError)
	author := fs.String("author", os.Getenv("PR_AUTHOR"), "the pull request's author login")
	holder := fs.String("expediter", os.Getenv("EXPEDITER_LOGIN"), "the login the standing order was given to")
	base := fs.String("base", "", "the pull request's base commit")
	head := fs.String("head", "", "the pull request's head commit")
	pin := fs.String("pin", "modules/applications/valheim/base/deployment.yaml", "the one file the order covers")
	_ = fs.Parse(args)

	switch {
	case *holder == "":
		fmt.Println("no standing order is configured (EXPEDITER_LOGIN is unset), so nothing is merged under one")
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

	problems := withinStandingOrder(nonEmptyLines(string(files)), changedLines(string(diff)), *pin)
	if len(problems) > 0 {
		fmt.Printf("REFUSED: %s changed more than its standing order covers.\n\n", *author)
		for _, p := range problems {
			fmt.Println("  " + p)
		}
		fmt.Printf("\nThe order covers the image digest and Steam build in %s, and nothing else.\n"+
			"Anything more needs a review, and the expediter should never have been able to write it.\n", *pin)
		return 1
	}
	fmt.Printf("%s changed only the pin it holds a standing order for\n", *author)
	return 0
}

var (
	pinnedImage = regexp.MustCompile(`^[+-]\s+image:\s+(\S+)@sha256:[0-9a-f]{64}\s*$`)
	steamBuild  = regexp.MustCompile(`^[+-]\s+[a-z0-9.-]+/steam-build:\s+"[0-9A-Za-z-]+"\s*$`)
)

// withinStandingOrder lists everything about a change that the order does not
// cover. An empty list is the only permitted answer.
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
	repos := map[string]bool{}
	for _, line := range changed {
		if m := pinnedImage.FindStringSubmatch(line); m != nil {
			repos[m[1]] = true
			continue
		}
		if steamBuild.MatchString(line) {
			continue
		}
		problems = append(problems, "changes a line the order does not cover: "+strings.TrimSpace(line))
	}
	if len(repos) > 1 {
		problems = append(problems, "moves the pin to a different image repository")
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
