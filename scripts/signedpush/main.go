// Command signedpush publishes the current branch as GitHub-signed commits.
//
// A GitHub App cannot hold a signing key - SSH and GPG signing keys are
// user-account resources, and this repository's agent deliberately has no user
// account. What an App can do is have GitHub sign on its behalf: a commit
// created through the Git Data API with an installation token comes back
// signed with GitHub's own key, attributed to the app. That is how Dependabot
// and Renovate produce verified commits, and it is what this does.
//
// The naive form of this uploads one blob per changed file, which is slow on a
// large diff. This does not. `git push` moves the objects in a single packfile
// to a ref outside refs/heads/, and because git and GitHub compute identical
// SHAs the local tree is then already addressable server-side - so the API
// work is a constant three calls no matter how many files changed.
//
//	git push -> refs/signing/<tmp>   one packfile, any diff size
//	POST /git/commits                GitHub signs it
//	POST or PATCH /git/refs          create or fast-forward the branch
//	DELETE the scratch ref
//
// Local workflow is untouched: commit as normal, hooks and commitlint included.
// Only the push changes. A plain `git push` is refused by the pre-push hook -
// see scripts/pushguard, which allows the scratch ref below and nothing that
// would update refs/heads directly.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	defaultAppID  = "4753166"
	defaultKeyEnv = "GITHUB_APP_KEY"
	defaultKey    = "/home/claude/.config/gh-app/homelab-agent.pem"
)

func main() {
	var (
		appID    = flag.String("app-id", envOr("GITHUB_APP_ID", defaultAppID), "GitHub App id.")
		keyPath  = flag.String("key", envOr(defaultKeyEnv, defaultKey), "Path to the App private key (PEM).")
		branch   = flag.String("branch", "", "Branch to publish. Defaults to the current one.")
		tokenOut = flag.Bool("token", false, "Print an installation access token and exit, for `gh auth login --with-token`.")
		dryRun   = flag.Bool("dry-run", false, "Say what would be published, contact GitHub only to read.")
	)
	flag.Parse()

	if err := run(*appID, *keyPath, *branch, *tokenOut, *dryRun); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func run(appID, keyPath, branch string, tokenOnly, dryRun bool) error {
	key, err := loadKey(keyPath)
	if err != nil {
		return err
	}
	jwt, err := appJWT(key, appID, time.Now())
	if err != nil {
		return err
	}
	httpc := &http.Client{Timeout: 30 * time.Second}
	token, err := installationToken(jwt, httpc)
	if err != nil {
		return err
	}

	// The token is written to stdout only when explicitly asked for, so it can
	// be piped straight into `gh auth login --with-token` without ever being
	// echoed by anything else here.
	if tokenOnly {
		fmt.Println(token)
		return nil
	}

	a := &api{token: token, http: httpc}

	remoteURL, err := git("remote", "get-url", "origin")
	if err != nil {
		return err
	}
	owner, repo, err := parseRemote(remoteURL)
	if err != nil {
		return err
	}

	if branch == "" {
		if branch, err = git("rev-parse", "--abbrev-ref", "HEAD"); err != nil {
			return err
		}
	}
	if branch == "HEAD" {
		return fmt.Errorf("HEAD is detached; name a branch with -branch")
	}

	headRef := "heads/" + branch
	baseSHA, branchExists, err := a.refSHA(owner, repo, headRef)
	if err != nil {
		return err
	}

	if branchExists {
		// The published commits are replicas - same trees, different SHAs,
		// because GitHub signs a commit it creates rather than the one that
		// was committed locally. So the remote tip is an object the local
		// clone may not have yet, and once it does have it, HEAD has to
		// actually descend from it or there is no common base to build on.
		// Both are recoverable, and neither should be guessed at.
		if err := ensureLocal(baseSHA, branch); err != nil {
			return err
		}
		if _, err := git("merge-base", "--is-ancestor", baseSHA, "HEAD"); err != nil {
			return fmt.Errorf(`%s has diverged from its remote.

The remote tip (%s) is not an ancestor of HEAD, which happens when a publish
half-completed or the branch was rewritten. Nothing here can pick the right
history for you:

    git fetch origin %s && git reset --hard origin/%s

will take the published side, discarding local commits that were never
published.

If the rewrite was deliberate - a rebase to clear a conflict or unstack a
branch - there is no force to reach for: the "all branches" ruleset carries
non_fast_forward, so GitHub refuses a rewrite on every branch here, for
everybody. The branch has to be deleted and recreated, and the pull request
recreated with it. See docs/epochs/01-ignition.md.`, branch, baseSHA[:8], branch, branch)
		}
	} else {
		// A new branch forks from wherever it actually diverged, not from
		// whatever main happens to be now.
		if baseSHA, err = git("merge-base", "origin/main", "HEAD"); err != nil {
			return fmt.Errorf("finding the merge base with origin/main: %w", err)
		}
	}

	revs, err := git("rev-list", "--reverse", baseSHA+"..HEAD")
	if err != nil {
		return err
	}
	commits := strings.Fields(revs)
	if err := refuseMerges(commits); err != nil {
		return err
	}
	if err := refuseStacked(branch); err != nil {
		return err
	}
	if len(commits) == 0 {
		fmt.Println("nothing to publish: the branch matches its remote")
		return nil
	}

	fmt.Printf("publishing %d commit(s) to %s/%s on %s\n", len(commits), owner, repo, branch)
	if dryRun {
		for _, c := range commits {
			subject, _ := git("log", "-1", "--format=%s", c)
			fmt.Printf("  %s  %s\n", c[:8], subject)
		}
		return nil
	}

	// One packfile, whatever the diff size. Outside refs/heads/ so no branch
	// appears mid-operation and no branch ruleset applies to it.
	scratch := "refs/signing/" + randomSuffix()
	if _, err := git("push", "--quiet", "origin", "HEAD:"+scratch); err != nil {
		return fmt.Errorf("staging objects: %w", err)
	}
	defer func() {
		// Best effort: a left-behind scratch ref is invisible and harmless,
		// and failing the publish over it would be worse.
		_ = a.deleteRef(owner, repo, strings.TrimPrefix(scratch, "refs/"))
	}()

	parent := baseSHA
	for _, c := range commits {
		tree, err := git("rev-parse", c+"^{tree}")
		if err != nil {
			return err
		}
		message, err := git("log", "-1", "--format=%B", c)
		if err != nil {
			return err
		}
		signed, err := a.createCommit(owner, repo, strings.TrimRight(message, "\n"), tree, []string{parent})
		if err != nil {
			return err
		}
		fmt.Printf("  %s -> %s  signed\n", c[:8], signed[:8])
		parent = signed
	}

	if err := a.setRef(owner, repo, headRef, parent, branchExists); err != nil {
		return err
	}

	// Move local onto the published history. Without this, local keeps the
	// unsigned originals while the remote has the signed replicas, the two
	// diverge permanently, and the next publish has no common base - which is
	// exactly the failure this program hit the first time it published itself.
	//
	// Content-neutral by construction: the signed commit carries the same tree,
	// so nothing in the working directory changes. It is still refused on a
	// dirty tree, because "no files change" is a property of the commits, not
	// a promise about uncommitted work.
	if err := syncLocal(branch, parent); err != nil {
		fmt.Fprintf(os.Stderr, "\npublished, but the local branch was left behind: %v\n", err)
		fmt.Fprintf(os.Stderr, "run: git fetch origin %s && git reset --hard origin/%s\n", branch, branch)
		return nil
	}
	fmt.Printf("%s is at %s, verified\n", branch, parent[:8])
	return nil
}

// refuseMerges stops a branch containing a merge commit from being published.
//
// This program replays commits as a linear chain: each signed commit gets one
// parent, the previously signed one. A merge commit has two, and flattening it
// does not fail - it silently produces replicas of everything the merge
// brought in, with new SHAs. The branch then contains duplicates of commits
// already on main, main is no longer an ancestor of it, and merging would
// write those duplicates into main's history.
//
// That happened once, to a branch that had `git merge origin/main` run on it.
// Nothing complained; the branch simply grew a second copy of three merged
// pull requests. Refusing is the honest behaviour until this replays merges
// properly, because the failure is invisible in the output and obvious only
// days later in the log.
func refuseMerges(commits []string) error {
	for _, c := range commits {
		parents, err := git("rev-list", "--parents", "-n", "1", c)
		if err != nil {
			return err
		}
		// "<sha> <parent> [<parent>...]" - more than two fields means a merge.
		if len(strings.Fields(parents)) > 2 {
			subject, _ := git("log", "-1", "--format=%s", c)
			return fmt.Errorf(`%s is a merge commit, and publishing replays commits as a linear chain.

    %s  %s

Flattening it would republish everything the merge brought in as new commits,
so the branch would carry duplicates of history already on main and main would
stop being an ancestor of it.

Rebase instead, which keeps the branch linear:

    git fetch origin && git rebase origin/main`, c[:8], c[:8], subject)
		}
	}
	return nil
}

// ensureLocal makes a remote object available locally, fetching only if it is
// actually missing.
func ensureLocal(sha, branch string) error {
	if _, err := git("cat-file", "-e", sha+"^{commit}"); err == nil {
		return nil
	}
	if _, err := git("fetch", "--quiet", "origin", branch); err != nil {
		return fmt.Errorf("fetching %s to learn the published history: %w", branch, err)
	}
	if _, err := git("cat-file", "-e", sha+"^{commit}"); err != nil {
		return fmt.Errorf("the remote tip %s is still unknown after fetching %s", sha[:8], branch)
	}
	return nil
}

func syncLocal(branch, sha string) error {
	// --untracked-files=no, because `git reset --hard` does not touch
	// untracked files and refusing over them is a false positive. The first
	// version did refuse: publishing from a branch that did not yet carry
	// scripts/signedpush left the built tool sitting untracked, which read as
	// a dirty tree and blocked a sync that would have been perfectly safe.
	// What must block it is a modified tracked file, which reset would discard.
	dirty, err := git("status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return err
	}
	if strings.TrimSpace(dirty) != "" {
		return fmt.Errorf("the working tree has uncommitted changes to tracked files")
	}
	if _, err := git("fetch", "--quiet", "origin", branch); err != nil {
		return err
	}
	if _, err := git("reset", "--hard", "--quiet", sha); err != nil {
		return err
	}
	return nil
}

// git runs a git command and, when it fails, says what git said.
//
// The stderr used to be discarded, and that cost a whole session. A publish
// failed with nothing but `staging objects: git push ... exit status 1`, which
// says only that something went wrong somewhere; four wrong theories followed,
// including one that had the operator checking a GitHub App permission that
// was never the problem. The line git had actually printed named the cause
// exactly:
//
//	! [remote rejected] HEAD -> refs/signing/...
//	  (refusing to allow a GitHub App to create or update workflow
//	   `.github/workflows/clerk.yml` without `workflows` permission)
//
// That is the boundary working as designed - the branch had been cut from a
// stale main, so relative to the new tip it reverted a workflow file - and it
// is a one-line diagnosis the moment anybody can read it.
//
// Nothing here is a secret. The push authenticates through git's credential
// helper rather than a URL, so the remote git prints carries no token, and the
// program holds the App key rather than passing it to a subprocess.
func git(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		said := strings.TrimSpace(stderr.String())
		if said == "" {
			return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return "", fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, said)
	}
	return strings.TrimRight(string(out), "\n"), nil
}

func randomSuffix() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// refuseStacked stops a branch built on another open branch from publishing.
//
// A branch should carry only its own work. Cut one from another feature
// branch instead of from main and it carries that branch's commits too - and
// when the other branch is squash-merged, those commits become one different
// commit with the same content. Merging this one then conflicts on every file
// they touched, and the person who sees the conflict is the one who did
// nothing wrong.
//
// That happened: a fix branch was cut while the previous fix branch was still
// checked out, and inherited its whole commit. The mistake is invisible at the
// time - the branch looks fine, the tests pass, the pull request reads
// correctly - and only surfaces days later as a conflict nobody can explain.
// It is also documented as a rule, and the rule was not enough, which is why
// this is code.
//
// The detection needs no GitHub API. A commit in origin/main..HEAD that some
// OTHER remote branch also contains was not written for this branch: had that
// branch merged, the commit would be on main and out of this range already.
//
// Epoch branches are exempt, and deliberately. The estate's model is that
// pieces of an epoch branch from it and target it, so a branch based on
// epoch/** carries that epoch's commits by design. Refusing those would refuse
// the documented way of working, which is the failure the push guard already
// made once by applying a rule to everybody it was not written for.
func refuseStacked(branch string) error {
	// Fails loudly rather than skipping: without origin/main there is no range
	// to check, and a guard that quietly checks nothing is worse than none.
	if _, err := git("rev-parse", "--verify", "origin/main"); err != nil {
		return fmt.Errorf("cannot check for a stacked branch: origin/main is not available locally (%w).\n\n    git fetch origin main", err)
	}
	revs, err := git("rev-list", "origin/main..HEAD")
	if err != nil {
		return err
	}
	for _, c := range strings.Fields(revs) {
		out, err := git("branch", "-r", "--contains", c, "--format=%(refname:short)")
		if err != nil {
			return err
		}
		for _, ref := range strings.Fields(out) {
			switch {
			case ref == "origin/main", ref == "origin/HEAD",
				ref == "origin/"+branch,
				strings.HasPrefix(ref, "origin/epoch/"):
				continue
			}
			subject, _ := git("log", "-1", "--format=%s", c)
			return fmt.Errorf(`%s is already on %s, so this branch is stacked on it.

    %s  %s

A branch should carry only its own work. When %s is squash-merged, that commit
becomes one different commit with the same content - and merging this branch
afterwards conflicts on every file they both touch.

Rebase onto main, dropping what belongs to the other branch:

    git fetch origin && git rebase --onto origin/main %s

If this genuinely belongs to an epoch branch, base it on that branch: epoch/**
is exempt, because pieces of an epoch are meant to build on it.`,
				c[:8], ref, c[:8], subject, ref, c[:8])
		}
	}
	return nil
}
