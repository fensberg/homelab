// Command pushguard refuses a plain `git push` that would update a branch.
//
// A GitHub App cannot hold a signing key, so commits are signed by GitHub on
// the App's behalf, through the Git Data API - which is what `task push`
// (scripts/signedpush) does. A plain `git push` also works, and produces
// unsigned commits attributed to whatever local git config happens to say.
// That was documented and it was not enough: a session pushed twice with
// plain git before anyone noticed, and the commits could not be repaired
// afterwards because `non_fast_forward` applies to feature branches here too.
//
// So the rule is enforced rather than described. The distinction is
// structural rather than a flag anyone can forget or fake:
//
//	signedpush pushes to refs/signing/<tmp>, a scratch ref, and then asks
//	GitHub to create the branch commit through the API.
//	A plain `git push` updates refs/heads/<branch> directly.
//
// Refusing any direct update of refs/heads leaves signedpush working exactly
// as before and stops every accidental route to an unsigned commit. There is
// no bypass environment variable on purpose: an escape hatch that exists to
// be used in a hurry is the thing that gets used in a hurry.
//
// THE RULE IS ABOUT THE COMMITS, NOT ABOUT WHO IS PUSHING.
//
// The first version refused every direct branch push, from anybody. That was a
// bug rather than strictness: signedpush reads the App key from a path inside
// the agent's mode-700 home, so `task push` fails for the human operator too,
// and refusing `git push` as well left them no way to publish at all. A guard
// that refuses the only available path is not a guard, it is an outage.
//
// The two parties sign by different means, and both are legitimate:
//
//   - The agent has no user account, because it deliberately holds none. SSH
//     and GPG signing keys are user-account resources, so it cannot sign
//     locally at all. signedpush pushes objects to refs/signing/ and has
//     GitHub create the commit through the API, which comes back signed with
//     GitHub's key.
//   - The human has a user account and can hold a signing key, so ordinary
//     `git commit -S` signs locally and a plain push is entirely correct.
//
// So this asks the question that actually matters - is what you are about to
// put on a branch signed - and lets each party answer it their own way. The
// scratch ref is allowed because signedpush signs afterwards, by construction.
//
// That is not an escape hatch either. Nobody can satisfy it by setting a
// variable; they satisfy it by signing.

// pre-commit runs this at the pre-push stage and sets PRE_COMMIT_REMOTE_BRANCH
// to the ref being updated. If that is absent the hook refuses rather than
// waves the push through, because a guard that fails open is not a guard.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// The ref namespace signedpush uses for its scratch push. Keep in step with
// scripts/signedpush/main.go, which builds "refs/signing/" + a random suffix.
const scratchPrefix = "refs/signing/"

// The ref namespace a branch update lands in, and the one to refuse.
const branchPrefix = "refs/heads/"

// remoteRefEnv is set by pre-commit at the pre-push stage. It carries the
// remote ref being updated, which is the only thing this needs to decide.
const remoteRefEnv = "PRE_COMMIT_REMOTE_BRANCH"

// pre-commit sets these at the pre-push stage: the remote's current tip and
// the local tip being pushed.
const (
	fromRefEnv = "PRE_COMMIT_FROM_REF"
	toRefEnv   = "PRE_COMMIT_TO_REF"
)

// unsignedCommits returns the commits in the push that carry no signature.
//
// It asks whether a signature is *present*, not whether it verifies, and the
// distinction is load bearing. `git log --format=%G?` reports N - the same
// value it reports for a genuinely unsigned commit - whenever it cannot check
// the signature, which for SSH signing is any repository without
// `gpg.ssh.allowedSignersFile` configured. Measured: a commit made with
// `commit.gpgsign` and a valid SSH key reports N, alongside
// "error: gpg.ssh.allowedSignersFile needs to be configured".
//
// So using %G? here would refuse exactly the commits somebody had just gone to
// the trouble of signing - the same shape of failure as the first version of
// this guard, which refused the only party doing the right thing. Whether a
// signature is *trusted* is GitHub's question and GitHub holds the keys to
// answer it. Whether one exists at all is this hook's question, and the commit
// object's gpgsig header answers it with no configuration whatsoever.
func unsignedCommits(from, to string) ([]string, error) {
	// No ref information is not "nothing to check". Without it there is no
	// range to read, and inferring one from whatever `git log` does with an
	// empty argument makes the answer depend on the machine rather than on the
	// push - which is exactly how a test came to pass for the wrong reason.
	if to == "" {
		return nil, errors.New("no ref information: cannot tell which commits this push would add")
	}

	rangeArg := from + ".." + to
	args := []string{"log", "--format=%H", rangeArg}
	// An all-zero from-ref means a new branch, where there is no range. Ask
	// instead for what this push would add that no remote already has.
	if from == "" || strings.Trim(from, "0") == "" {
		args = []string{"log", "--format=%H", to, "--not", "--remotes=origin"}
	}

	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("could not list the commits being pushed: %w", err)
	}

	var unsigned []string
	for _, sha := range strings.Fields(string(out)) {
		signed, err := hasSignature(sha)
		if err != nil {
			return nil, err
		}
		if !signed {
			unsigned = append(unsigned, sha[:min(8, len(sha))])
		}
	}
	return unsigned, nil
}

// hasSignature reports whether a commit object carries a signature header.
// Both spellings are checked: `gpgsig` for the commit's own signature and
// `gpgsig-sha256` for the object-format variant.
func hasSignature(sha string) (bool, error) {
	// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
	out, err := exec.Command("git", "cat-file", "commit", sha).Output()
	if err != nil {
		return false, fmt.Errorf("could not read commit %s: %w", sha[:min(8, len(sha))], err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			break // end of the header block
		}
		if strings.HasPrefix(line, "gpgsig") {
			return true, nil
		}
	}
	return false, nil
}

func main() {
	if err := run(os.Getenv(remoteRefEnv)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ref string) error {
	switch {
	case strings.HasPrefix(ref, scratchPrefix):
		// signedpush moving objects into place. This is the supported path.
		return nil

	case strings.HasPrefix(ref, branchPrefix):
		// A branch update is only refused when it would put unsigned commits
		// on the branch. Signing locally is a perfectly good way to satisfy
		// this and is the way available to anyone with a user account;
		// `task push` is how the agent satisfies it, not the only route.
		unsigned, err := unsignedCommits(os.Getenv(fromRefEnv), os.Getenv(toRefEnv))
		if err != nil {
			return fmt.Errorf("%s", cannotTell(err))
		}
		if len(unsigned) == 0 {
			return nil
		}
		return fmt.Errorf("%s", refused(strings.TrimPrefix(ref, branchPrefix), unsigned))

	case ref == "":
		return fmt.Errorf("%s", undetermined())

	default:
		// Tags and anything else that is not a branch update: not this
		// hook's business, and not a route to an unsigned branch commit.
		return nil
	}
}

func refused(branch string, unsigned []string) string {
	lines := []string{
		"",
		"  " + plural(len(unsigned)) + " on " + branchPrefix + branch + ":",
		"",
	}
	for _, sha := range unsigned {
		lines = append(lines, "      "+sha)
	}
	lines = append(lines,
		"",
		"  Sign them, by whichever route is yours:",
		"",
		"  * If you have a GitHub account, sign locally. This is the ordinary",
		"    way and needs nothing from this repository:",
		"",
		"        git config --global gpg.format ssh",
		"        git config --global user.signingkey ~/.ssh/id_ed25519.pub",
		"        git config --global commit.gpgsign true",
		"",
		"    Then add that same public key to GitHub a second time, as a",
		"    Signing key rather than an Authentication key, and amend or",
		"    rebase to re-sign what is already committed.",
		"",
		"  * If you are the agent, use `task push`. It has no user account and",
		"    therefore no signing key, so GitHub signs on its behalf through",
		"    the API instead.",
		"",
		"  This cannot be fixed after the fact: `non_fast_forward` applies to",
		"  feature branches here, so an unsigned commit cannot be rewritten by",
		"  anyone once it is published.",
		"",
	)
	return strings.Join(lines, "\n")
}

func plural(n int) string {
	if n == 1 {
		return "1 commit carries no signature"
	}
	return fmt.Sprintf("%d commits carry no signature", n)
}

// cannotTell fails closed. Being unable to read the commits is not evidence
// that they are signed, and a guard that waves a push through because it could
// not look is not a guard.
func cannotTell(err error) string {
	return strings.Join([]string{
		"",
		"  Could not determine whether the commits being pushed are signed:",
		"      " + err.Error(),
		"",
		"  Refusing rather than assuming. If this is wrong, it is a defect in",
		"  the guard rather than in the push.",
		"",
	}, "\n")
}

func undetermined() string {
	return strings.Join([]string{
		"",
		"  Cannot tell which ref this push would update: " + remoteRefEnv,
		"  is unset.",
		"",
		"  Refusing rather than allowing it, because the thing being guarded",
		"  against is unsigned commits reaching a branch, and a guard that",
		"  fails open does not guard anything.",
		"",
		"  Use `task push`. If this fired during `task push` itself, that is a",
		"  defect in this hook rather than in the push - pre-commit sets this",
		"  variable from 4.x on.",
		"",
	}, "\n")
}
