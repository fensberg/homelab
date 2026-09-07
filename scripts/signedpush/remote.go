package main

import (
	"fmt"
	"strings"
	"unicode"
)

// parseRemote extracts owner and repo from whatever `git remote get-url` says.
//
// It deliberately never quotes the URL back, in an error or anywhere else: a
// remote may legitimately carry a credential (https://x-access-token:...@),
// and an error message is one of the easiest places for one to escape.
func parseRemote(url string) (owner, repo string, err error) {
	bad := fmt.Errorf("origin does not look like a github.com repository")

	s := strings.TrimSpace(url)
	if s == "" {
		return "", "", bad
	}

	// scp-style: git@github.com:owner/repo.git
	if host, path, ok := strings.Cut(s, ":"); ok && !strings.Contains(host, "/") {
		if strings.HasSuffix(host, "github.com") {
			return splitPath(path, bad)
		}
	}

	// URL forms: https://…, ssh://…, with or without credentials.
	if _, rest, ok := strings.Cut(s, "://"); ok {
		hostAndPath := rest
		// Drop any userinfo. This is the credential-bearing part.
		if _, after, found := strings.Cut(hostAndPath, "@"); found {
			hostAndPath = after
		}
		host, path, found := strings.Cut(hostAndPath, "/")
		if !found || host != "github.com" {
			return "", "", bad
		}
		return splitPath(path, bad)
	}

	return "", "", bad
}

func splitPath(path string, bad error) (string, string, error) {
	path = strings.Trim(path, "/")
	owner, repo, ok := strings.Cut(path, "/")
	if !ok || owner == "" || repo == "" {
		return "", "", bad
	}
	// Only the .git suffix, not every dot - "home.lab" is a legal repo name.
	repo = strings.TrimSuffix(repo, ".git")
	if repo == "" || strings.Contains(repo, "/") {
		return "", "", bad
	}
	// Both halves are interpolated into an API path - "/repos/<owner>/<repo>/…"
	// - so a value that is not a single path segment does not fail, it
	// addresses something else. `https://github.com/../repo` parsed to
	// owner ".." before this, which resolves one level up from /repos.
	//
	// Found by FuzzParseRemote rather than by review, which is the argument
	// for the fuzz tier in one line: the table of URLs somebody thinks of does
	// not contain the ones that break it.
	for _, segment := range []string{owner, repo} {
		if !isOneSegment(segment) {
			return "", "", bad
		}
	}
	return owner, repo, nil
}

// isOneSegment reports whether s can stand as a single path segment: not a
// traversal, no separator, nothing a URL would have to escape.
func isOneSegment(s string) bool {
	if s == "." || s == ".." {
		return false
	}
	for _, r := range s {
		if r == '/' || r == '\\' || unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}
