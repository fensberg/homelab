package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"

	"homelab/details/console"
	"homelab/details/repopath"
)

// guard-secrets refuses a secret in the commits between two points.
//
// WHY THIS EXISTS. TruffleHog ran in one place, the Secrets Scan lane, and
// that is after the push. A push here cannot be taken back: the repository is
// public, and the rulesets refuse a rewrite of any branch, so a finding on a
// pushed commit costs a new branch and a new pull request. An example written
// to be refused cost exactly that (#778), with gitleaks at commit passing it.
// So the scan runs before the push as well as on the pull request, and both
// run it from here: one definition, so the two cannot come to differ.
//
// It verifies. TruffleHog presents a candidate to the API of the vendor that
// issued it, which is what tells a live key from a format - the lane always
// did, and a scan that did less before the push would pass what the lane then
// refuses.
//
// It refuses not knowing. No scanner, or a scan that did not finish, is an
// error, never a clean answer.

// secretsScanner is the program that scans, delivered by
// scripts/take-delivery.sh.
const secretsScanner = "trufflehog"

// secretsFound is the status TruffleHog's --fail exits with when it found
// something.
const secretsFound = 183

func guardSecrets(args []string) int {
	return guardSecretsTo(args, console.InActions(), os.Stdout, os.Stderr)
}

func guardSecretsTo(args []string, annotate bool, out, errs io.Writer) int {
	fs := flag.NewFlagSet("guard-secrets", flag.ContinueOnError)
	fs.SetOutput(errs)
	base := fs.String("base", "", "the commit the scan starts after")
	head := fs.String("head", "", "the commit the scan ends at")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *base == "" || *head == "" {
		fmt.Fprintln(errs, "security guard-secrets: need -base and -head, the commits the scan runs between")
		return 2
	}
	// The whole checkout, wherever in it this was started: go run -C starts
	// it in its own directory.
	top, err := repopath.Here()
	if err != nil {
		fmt.Fprintln(errs, "security guard-secrets: "+err.Error())
		return 1
	}
	if _, err := exec.LookPath(secretsScanner); err != nil {
		fmt.Fprintln(errs, "security guard-secrets: trufflehog is not on PATH, so nothing was scanned; take delivery of it: scripts/take-delivery.sh trufflehog")
		return 1
	}
	// proved by TestEveryProgramRunIsNamedByAConstantOrIsTheOperators
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	cmd := exec.Command(secretsScanner, scanArgs(top, *base, *head, annotate)...)
	cmd.Stdout, cmd.Stderr = out, errs
	var exit *exec.ExitError
	switch err := cmd.Run(); {
	case err == nil:
		return 0
	case errors.As(err, &exit) && exit.ExitCode() == secretsFound:
		fmt.Fprintf(errs, `security guard-secrets: a secret, or what may be one, is in the commits after %s.

Take it out of those commits before they are pushed: a pushed commit cannot be
rewritten here, and a secret that reaches GitHub costs a new branch and a new
pull request. If it was verified it is live, so rotate it as well. If it is an
example written to be refused, write it as a format - %%s where the value goes.
`, *base)
		return 1
	default:
		fmt.Fprintln(errs, "security guard-secrets: the scan stopped short, so nothing is known about these commits: "+err.Error())
		return 1
	}
}

// scanArgs is the scan, in full: every commit after base up to head, each
// candidate verified, and anything verified or not disprovable a finding.
func scanArgs(top, base, head string, annotate bool) []string {
	args := []string{"git", "file://" + top, "--since-commit", base, "--branch", head, "--fail", "--no-update", "--results=verified,unknown"}
	if annotate {
		// The same scan; its findings written where a pull request shows them.
		args = append(args, "--github-actions")
	}
	return args
}
