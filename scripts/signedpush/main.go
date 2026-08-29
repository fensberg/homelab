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
	if !branchExists {
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
	fmt.Printf("%s is at %s, verified\n", branch, parent[:8])
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
