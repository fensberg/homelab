package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// The two files this compares, and the cache it enumerates. Paths rather than
// flags: this runs from a commit hook, and a hook that can be pointed at a
// different list is a hook that can be pointed at an empty one.
const (
	configPath    = ".pre-commit-config.yaml"
	suppliersPath = "scripts/approved-suppliers.yml"
)

func main() {
	configured, err := reposIn(configPath, "- repo:")
	if err != nil {
		fail("reading " + configPath + ": " + err.Error())
	}
	approved, err := reposIn(suppliersPath, "- repo:")
	if err != nil {
		fail("reading " + suppliersPath + ": " + err.Error())
	}
	if len(approved) == 0 {
		// An empty approved list would make every hook a finding, which reads
		// as the guard being broken rather than as an empty list - and the
		// person reading it would disable the guard.
		fail("no suppliers are declared under 'hooks:' in " + suppliersPath +
			", so this cannot tell an approved repository from an unapproved one")
	}

	cached, err := cachedRepos()
	if err != nil {
		// Fail closed. Not being able to read the cache is not the same as the
		// cache being clean, and the cache is the half nothing else can see.
		fail("could not read the pre-commit cache, so this commit cannot be shown to be " +
			"running only approved hooks: " + err.Error())
	}

	findings := Check(configured, cached, approved)
	if len(findings) == 0 {
		return
	}
	fmt.Fprint(os.Stderr, Explain(findings))
	os.Exit(1)
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "supplierguard: "+msg)
	os.Exit(1)
}

// reposIn pulls `- repo: <url>` values out of a YAML file without a parser.
//
// No dependency on purpose: this runs before every commit, on both a
// workstation and a runner, and the one obvious library for the job is one
// more supplier for a program whose entire subject is suppliers.
func reposIn(path, key string) ([]string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(body), "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, key) {
			continue
		}
		v := strings.TrimSpace(strings.TrimPrefix(t, key))
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
			// this guard can vouch for. Reported as a finding rather than
			// skipped: "I could not tell what this is" and "this is fine" are
			// different answers.
			out = append(out, dir+" (no git remote)")
			continue
		}
		out = append(out, strings.TrimSpace(string(remote)))
	}
	return out, nil
}
