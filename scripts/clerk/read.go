package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// tracked returns the paths git knows about, and refuses everything else.
//
// This is the rule the whole free-tier argument rests on. The price of the
// free tier is that content is used to improve the vendor's products, and that
// costs this estate nothing only because everything sent is already
// world-readable - a public repository, with secrets kept out of git by
// construction. The moment this program reads a rendered config, a run log or
// anything off the Sterilize path, that stops being true.
//
// So it is enforced here rather than written down as a caution: git is the
// definition of what is public, and `git ls-files` is the definition of what
// git has. An untracked file in the working tree - a rendered
// site.auto.yml, a kubeconfig, a plan output - is exactly the class this
// refuses, and exactly the class that would be sitting there during a real run.
func tracked(root string, paths []string) ([]string, error) {
	args := append([]string{"-C", root, "ls-files", "-z", "--"}, paths...)
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("asking git what it tracks: %w", err)
	}

	var found []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			found = append(found, p)
		}
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("git tracks none of %v. The clerk reads only what is committed, because that is what is already public", paths)
	}
	sort.Strings(found)
	return found, nil
}

// A bundle is one slice of the repository, split into what runs and what was
// claimed about it.
type bundle struct {
	code     string         // comments blanked, every line numbered
	prose    string         // the commentary that was taken out
	lines    map[string]int // path -> line count, for checking a citation
	included []string
}

// read builds a bundle from tracked files, bounded by a byte budget.
//
// Bounded because the free tier allows 250,000 tokens a minute and a prompt
// that exceeds it fails the whole call rather than the last file. It stops
// before the limit rather than truncating through it: a prompt cut mid-function
// makes the model describe something that does not exist, confidently.
func read(root string, paths []string, budget int) (*bundle, error) {
	b := &bundle{lines: map[string]int{}}
	var code, prose strings.Builder

	for _, p := range paths {
		body, err := os.ReadFile(filepath.Join(root, p))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", p, err)
		}

		blanked, claims, runs := split(p, string(body))
		var chunk string
		if runs {
			chunk = "\n=== " + p + " ===\n" + number(blanked) + "\n"
		}
		claimChunk := ""
		if strings.TrimSpace(claims) != "" {
			claimChunk = "\n=== written about " + p + " ===\n" + claims + "\n"
		}

		if code.Len()+len(chunk)+prose.Len()+len(claimChunk) > budget {
			break
		}
		code.WriteString(chunk)
		prose.WriteString(claimChunk)
		b.lines[p] = strings.Count(string(body), "\n") + 1
		b.included = append(b.included, p)
	}

	if len(b.included) == 0 {
		return nil, fmt.Errorf("the first tracked file alone exceeds the %d byte budget; ask for a smaller slice", budget)
	}
	b.code, b.prose = code.String(), prose.String()
	return b, nil
}

// number prefixes every line, so a citation can be exact rather than
// approximate. The numbers are the file's own, which is why split blanks the
// commentary rather than deleting it.
func number(body string) string {
	var out strings.Builder
	for i, line := range strings.Split(body, "\n") {
		fmt.Fprintf(&out, "%d| %s\n", i+1, line)
	}
	return out.String()
}
