// guard-deliveries refuses to let a commit proceed while an undeclared
// third-party repository is installed as a commit hook.
//
// Default deny. Not "a test in CI notices afterwards" - by then the code has
// run on the machine holding the vault session, and the finding is archaeology.
// This runs first, before any hook does its work, and stops the commit.
//
// Two questions, and the second is the one nothing else could answer:
//
//	is every repo in .pre-commit-config.yaml declared as a supplier?
//	is every repo in the local cache declared as a supplier?
//
// The config can be read by anybody. The cache cannot: pre-commit clones into
// ~/.cache/pre-commit under generated names like `repornkulz89`, so an eighth
// repository sitting there looks exactly like the seven that belong. Answering
// that by hand needs somebody to notice a strange directory and know how
// pre-commit names things, which is not a control.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Finding is one repository that is installed or configured and not approved.
type Finding struct {
	Repo  string
	Where string // "the hook configuration" or "the local cache"
}

// Check compares what is configured and what is installed against what has been
// approved.
//
// Declared-but-unused is deliberately not a finding here. That is a tidiness
// problem for the test suite, and this runs on every commit: a guard that
// refuses a commit over an unused entry in a list is a guard somebody disables.
func Check(configured, cached, approved []string) []Finding {
	ok := map[string]bool{}
	for _, a := range approved {
		ok[normalise(a)] = true
	}

	seen := map[string]bool{}
	var out []Finding
	for _, r := range configured {
		if n := normalise(r); n != "" && !ok[n] && !seen["c"+n] {
			seen["c"+n] = true
			out = append(out, Finding{Repo: r, Where: "the hook configuration"})
		}
	}
	for _, r := range cached {
		if n := normalise(r); n != "" && !ok[n] && !seen["k"+n] {
			seen["k"+n] = true
			out = append(out, Finding{Repo: r, Where: "the local cache"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Repo < out[j].Repo })
	return out
}

// normalise makes two spellings of one repository compare equal. A remote can
// carry a .git suffix, a trailing slash, or an ssh scheme, and none of those
// make it a different supplier - while treating them as different would refuse
// a commit over punctuation and get the guard switched off.
func normalise(repo string) string {
	r := strings.TrimSpace(repo)
	r = strings.TrimSuffix(r, "/")
	r = strings.TrimSuffix(r, ".git")
	r = strings.TrimPrefix(r, "https://")
	r = strings.TrimPrefix(r, "http://")
	r = strings.TrimPrefix(r, "git@")
	r = strings.ReplaceAll(r, "github.com:", "github.com/")
	return strings.ToLower(r)
}

// Explain is what the operator reads when the commit is refused.
func Explain(findings []Finding) string {
	var b strings.Builder
	b.WriteString("refusing this commit: an unapproved supplier is installed as a commit hook.\n\n")
	for _, f := range findings {
		fmt.Fprintf(&b, "  %s\n      found in %s\n", f.Repo, f.Where)
	}
	b.WriteString(`
Every repository listed above runs on this machine, on every commit, with your
own privileges - not in a container and not in CI. That is a better position
than most software gets, and it is why nothing arrives here without somebody
deciding it should.

This is a speed bump with a named price, not a wall. An unapproved tool is
welcome here; adopting one costs a pull request that does nothing else, saying
what it is, who publishes it, what it would run with, what it replaces, and why
this estate should take delivery from that party from now on. On its own,
because bundled into a larger change the supply-chain decision becomes the
least visible thing in the diff - which is the failure this list exists to
prevent. Then declare it under 'hooks:' in scripts/approved-suppliers.yml.

If it does not belong, remove the hook - and if it is in the cache but not the
configuration, it is left over from a hook that used to be here, which
'pre-commit gc' removes.
`)
	return b.String()
}

// --- the verb ---------------------------------------------------------------

// The two files this compares, and the cache it enumerates. Fixed paths rather
// than flags: this runs from a commit hook, and a guard that can be pointed at
// a different list is a guard that can be pointed at an empty one.
const (
	hookConfigPath = ".pre-commit-config.yaml"
	suppliersPath  = "scripts/approved-suppliers.yml"
)

// repositoryRoot walks up from the working directory to the checkout that
// contains it. `.git` is the marker, and it is a file in a worktree and a
// directory in a normal clone - os.Stat does not care which.
func repositoryRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("could not determine the working directory: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("ran from %s, which is not inside a git checkout, "+
				"so there is no hook configuration to check", dir)
		}
		dir = parent
	}
}

func guardDeliveries(args []string) int {
	fs := flag.NewFlagSet("guard-deliveries", flag.ContinueOnError)
	// The gate has to be able to stand in front of the delivery as well as
	// behind it.
	//
	// pre-commit installs every hook repository - cloning it and building its
	// environment, which runs setup code - BEFORE it runs any hook. So a guard
	// that is itself a hook always reports after the download it exists to
	// prevent. On a workstation that is solved by githooks/pre-commit, which
	// git invokes first. On a runner there is no equivalent: the lane's first
	// act is `pre-commit run`, and by the time anything could object, seven
	// third-party repositories have been fetched and executed.
	//
	// This flag is the runner's version. It answers only the question that can
	// be answered before an install - is every configured repository approved -
	// and deliberately does not look at the cache, because the point is to run
	// when there is not one yet.
	beforeInstall := fs.Bool("before-install", false,
		"check only what is configured, for use before anything has been downloaded")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	// Resolved from the repository root rather than the working directory.
	//
	// The hook runs this from the root, so relative paths worked and nothing
	// said they were load-bearing. CI runs it as
	// `go run -C scripts/gatehouse .` - because each program is its own
	// module and there is nothing at the root to resolve a package path
	// against - which puts the working directory inside the module, and the
	// gate failed with "no such file or directory" instead of a verdict.
	//
	// A guard that only works from one directory is a guard with an
	// undeclared input. Find the root and stop caring.
	root, err := repositoryRoot()
	if err != nil {
		return refuse(err.Error())
	}

	configured, err := reposIn(filepath.Join(root, hookConfigPath))
	if err != nil {
		return refuse("reading " + hookConfigPath + ": " + err.Error())
	}
	approved, err := reposIn(filepath.Join(root, suppliersPath))
	if err != nil {
		return refuse("reading " + suppliersPath + ": " + err.Error())
	}
	if len(approved) == 0 {
		// An empty approved list would make every hook a finding, which reads
		// as the guard being broken rather than as an empty list - and the
		// person reading it would switch the guard off.
		return refuse("no suppliers are declared under 'hooks:' in " + suppliersPath +
			", so this cannot tell an approved repository from an unapproved one")
	}

	var cached []string
	if !*beforeInstall {
		cached, err = cachedRepos()
		if err != nil {
			// Fail closed. Not being able to read the cache is not the same as
			// the cache being clean, and the cache is the half nothing else
			// can see.
			return refuse("could not read the pre-commit cache, so this commit cannot be " +
				"shown to be running only approved hooks: " + err.Error())
		}
	}

	findings := Check(configured, cached, approved)
	if len(findings) == 0 {
		return 0
	}
	fmt.Fprint(os.Stderr, Explain(findings))
	return 1
}

func refuse(msg string) int {
	fmt.Fprintln(os.Stderr, "gatehouse guard-deliveries: "+msg)
	return 1
}

// reposIn pulls `- repo: <url>` values out of a YAML file without a parser.
//
// No dependency on purpose: this runs before every commit, and the obvious
// library for the job would be one more supplier for a program whose entire
// subject is suppliers.
func reposIn(path string) ([]string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(body), "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "- repo:") {
			continue
		}
		v := strings.TrimSpace(strings.TrimPrefix(t, "- repo:"))
		v = strings.Trim(v, `"'`)
		// `local` is this repository's own hooks: scripts already in this tree,
		// reviewed like everything else, and not a delivery from anybody.
		if v == "" || v == "local" || strings.HasPrefix(v, "#") {
			continue
		}
		out = append(out, v)
	}
	return out, nil
}

// cachedRepos asks each clone in the pre-commit cache where it came from.
//
// This is the question that could not be answered by inspection. An absent
// cache is not an error - a machine that has never run the hooks has none -
// but a cache that cannot be listed is, because that is indistinguishable from
// one holding something unapproved.
func cachedRepos() ([]string, error) {
	root := os.Getenv("PRE_COMMIT_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		root = filepath.Join(home, ".cache", "pre-commit")
	}

	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var out []string
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "repo") {
			continue
		}
		dir := filepath.Join(root, e.Name())
		// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
		remote, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
		if err != nil {
			// A directory under the cache with no git remote is not a clone
			// this guard can vouch for. Reported rather than skipped: "I could
			// not tell what this is" and "this is fine" are different answers.
			out = append(out, dir+" (no git remote)")
			continue
		}
		out = append(out, strings.TrimSpace(string(remote)))
	}
	return out, nil
}
