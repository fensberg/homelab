package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"homelab/details/gitenv"
	"homelab/details/pins"
	"homelab/details/repopath"
	"homelab/details/tofufiles"
)

// deliver-modules brings a change to what a site runs to the estate's gate.
//
// A site runs the estate's modules as they were at a pinned commit, so a
// change to a module reaches no site when it merges. It reaches them when
// the estate's default pin moves, and moving it was a line somebody edited
// by hand in a second pull request (#602). This is that edit: after a merge
// it moves the default to the merged commit, in the text it is given, when
// anything a pinned tree is read for differs between the two - the modules,
// and whatever they reach outside themselves (homelab/details/pins) - and
// leaves the text alone when nothing does. The workflow around it opens the
// pull request, as it does for a release.
//
// Merging that pull request is what moves the sites, and it stays a person's
// decision: the pull request's plan shows what the move would do to each
// site, and superintendent enforce-standing-order never passes it for a
// merge without review. A site held at a commit of its own is not moved.
//
// Text in and text out, like deliver: it changes one line, so the diff a
// person reviews is the move and nothing beside it.
func deliverModules(args []string) int {
	fs := flag.NewFlagSet("deliver-modules", flag.ContinueOnError)
	file := fs.String("pins", "", "the pins file to move the default in")
	to := fs.String("to", "", "the commit that has just merged")
	root := fs.String("root", "", "the repository to read (default: this one)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *file == "" || *to == "" {
		fmt.Fprintln(os.Stderr, "procurement deliver-modules: -pins and -to are both required")
		return 2
	}
	if *root == "" {
		found, err := repopath.Root()
		if err != nil {
			fmt.Fprintln(os.Stderr, "procurement deliver-modules:", err)
			return 1
		}
		*root = found
	}
	git := func(args ...string) ([]byte, error) {
		// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
		cmd := exec.Command("git", append([]string{"-C", *root}, args...)...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
		}
		return out, nil
	}
	outcome, err := moveDefaultPin(*root, *file, *to, git)
	if err != nil {
		fmt.Fprintln(os.Stderr, "procurement deliver-modules:", err)
		return 1
	}
	fmt.Println(outcome)
	return 0
}

// moveDefaultPin moves the default in the pins file at path to a commit when
// what a site reads differs between the two, and says what it did.
func moveDefaultPin(root, path, to string, git pins.Git) (string, error) {
	if !gitenv.IsCommit(to) {
		return "", fmt.Errorf("%q is not a full commit hash, and a pin is nothing else", to)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	current, err := pins.Parse(body)
	if err != nil {
		return "", err
	}
	if current.Default == to {
		return "the default pin is already " + to[:7], nil
	}
	// A checkout made for one commit does not hold every other: a runner's
	// is shallow, and a commit a squash merge left behind is on no branch.
	for _, commit := range []string{current.Default, to} {
		if _, err := git("cat-file", "-e", commit+"^{commit}"); err != nil {
			if _, err := git("fetch", "--quiet", "--no-tags", "--depth", "1", "origin", commit); err != nil {
				return "", fmt.Errorf("this checkout does not hold %s and it could not be fetched, so there is no telling whether the pin should move: %w", commit, err)
			}
		}
	}
	files, err := tofufiles.Read(root)
	if err != nil {
		return "", err
	}
	reads, err := pins.Reads(files)
	if err != nil {
		return "", err
	}
	differ, err := pins.Changed(git, current.Default, to, reads)
	if err != nil {
		return "", err
	}
	if len(differ) == 0 {
		return fmt.Sprintf("nothing a site reads differs between %s and %s, so the pin stays", current.Default[:7], to[:7]), nil
	}
	moved, err := pins.MoveDefault(body, to)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, moved, 0o644); err != nil {
		return "", err
	}
	shown := differ
	if len(shown) > 5 {
		shown = append(append([]string{}, differ[:5]...), fmt.Sprintf("and %d more", len(differ)-5))
	}
	return fmt.Sprintf("moved the default pin from %s to %s: %s", current.Default[:7], to[:7], strings.Join(shown, ", ")), nil
}
