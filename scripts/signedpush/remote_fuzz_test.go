package main

import (
	"strings"
	"testing"
	"unicode"
)

// The remote URL becomes part of an API path, so what comes out of it matters
// more than whether it parses.
//
// parseRemote reads `git remote get-url origin` and hands owner and repo to
// every API call this program makes - `/repos/<owner>/<repo>/git/refs` and the
// rest. Anything that survives parsing is interpolated into a URL without
// further checking, so a value containing a path separator or a traversal
// segment does not fail: it addresses a different endpoint.
//
// A table of URLs somebody thought of cannot find that. The inputs that break
// a parser are the ones nobody thought of, which is what this is for.
func FuzzParseRemote(f *testing.F) {
	for _, seed := range []string{
		"https://github.com/owner/repo.git",
		"https://github.com/owner/repo",
		"git@github.com:owner/repo.git",
		"ssh://git@github.com/owner/repo.git",
		"https://user:token@github.com/owner/repo.git",
		"https://gitlab.com/owner/repo.git",
		"", "://", "git@github.com:", "https://github.com//",
		"https://github.com/../repo", "https://github.com/owner/..",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, url string) {
		owner, repo, err := parseRemote(url)
		if err != nil {
			// A refusal is always a fine answer, and it must say nothing else.
			if owner != "" || repo != "" {
				t.Errorf("refused %q but still returned owner=%q repo=%q; a caller checking only the error would use them", url, owner, repo)
			}
			return
		}

		for name, value := range map[string]string{"owner": owner, "repo": repo} {
			if value == "" {
				t.Errorf("accepted %q with an empty %s", url, name)
			}
			if value == "." || value == ".." {
				t.Errorf(`accepted %q with %s=%q. That is interpolated into "/repos/<owner>/<repo>/…", so it addresses a different endpoint rather than failing.`, url, name, value)
			}
			if strings.ContainsAny(value, `/\`) {
				t.Errorf("accepted %q with %s=%q, which contains a path separator and so escapes the path segment it is meant to fill", url, name, value)
			}
			for _, r := range value {
				if unicode.IsSpace(r) || unicode.IsControl(r) {
					t.Errorf("accepted %q with %s=%q, which contains whitespace or a control character", url, name, value)
					break
				}
			}
		}
	})
}
