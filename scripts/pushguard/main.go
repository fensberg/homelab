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
// pre-commit runs this at the pre-push stage and sets PRE_COMMIT_REMOTE_BRANCH
// to the ref being updated. If that is absent the hook refuses rather than
// waves the push through, because a guard that fails open is not a guard.
package main

import (
	"fmt"
	"os"
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
		return fmt.Errorf("%s", refused(strings.TrimPrefix(ref, branchPrefix)))

	case ref == "":
		return fmt.Errorf("%s", undetermined())

	default:
		// Tags and anything else that is not a branch update: not this
		// hook's business, and not a route to an unsigned branch commit.
		return nil
	}
}

func refused(branch string) string {
	return strings.Join([]string{
		"",
		"  A plain `git push` would update " + branchPrefix + branch + " directly.",
		"",
		"  That produces unsigned commits, attributed to whatever local git",
		"  config says rather than to the App. Use:",
		"",
		"      task push",
		"",
		"  which pushes the objects to a scratch ref and has GitHub create and",
		"  sign the branch commit. Commit locally as normal - only the push",
		"  changes.",
		"",
		"  This cannot be fixed after the fact: `non_fast_forward` applies to",
		"  feature branches here, so an unsigned commit cannot be rewritten by",
		"  anyone, agent or admin.",
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
