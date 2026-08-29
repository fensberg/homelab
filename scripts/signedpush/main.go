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
// Only the push changes. A plain `git push` still works and produces unsigned
// commits, which is a visible failure rather than a silent one.
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
published`, branch, baseSHA[:8], branch, branch)
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
	dirty, err := git("status", "--porcelain")
	if err != nil {
		return err
	}
	if strings.TrimSpace(dirty) != "" {
		return fmt.Errorf("the working tree has uncommitted changes")
	}
	if _, err := git("fetch", "--quiet", "origin", branch); err != nil {
		return err
	}
	if _, err := git("reset", "--hard", "--quiet", sha); err != nil {
		return err
	}
	return nil
}

func git(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
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
