package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// What the clerk is asked, and the one thing it is allowed to read.
//
// The question is deliberately not "where do the docs and the code disagree".
// That builds an accusation engine, and this estate already rejected reviewers
// whose wrong findings cost more than their right ones. The clerk is asked to
// describe what the code does, having never seen what we claim it does. Where
// its description and ours differ, the operator sees it. Where it misreads,
// the misreading is itself the finding - a no-context reader getting it wrong
// says something about the code or its naming.
const accountPrompt = `You are reading part of a repository you have never seen before and know nothing about.

Write a short, plain account of what this code does: what it is for, what it would do when it runs, and anything a stranger would need to know to use or change it safely.

Rules:
- Describe only what is in front of you. Do not guess at intent you cannot see.
- Every claim must cite a file and a line, as path:line. A claim you cannot cite, do not make.
- If something is unclear or looks wrong to you, say so plainly and cite it.
- Do not praise, do not summarise your own answer, and do not offer to help further.

The files follow.
`

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
// refuses, and it is also exactly the class that would otherwise be sitting
// there during a real run.
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

// gather reads the tracked files into one prompt.
//
// Bounded, because the free tier allows 250,000 tokens a minute and a prompt
// that exceeds it fails the whole call rather than the last file. The limit is
// in bytes and deliberately conservative: it is better to describe a directory
// in two passes than to have one silently truncated in the middle of a
// function.
func gather(root string, paths []string, budget int) (string, []string, error) {
	var b strings.Builder
	var included []string

	b.WriteString(accountPrompt)
	for _, p := range paths {
		body, err := os.ReadFile(filepath.Join(root, p))
		if err != nil {
			return "", nil, fmt.Errorf("reading %s: %w", p, err)
		}
		chunk := "\n=== " + p + " ===\n" + string(body) + "\n"
		if b.Len()+len(chunk) > budget {
			break
		}
		b.WriteString(chunk)
		included = append(included, p)
	}

	if len(included) == 0 {
		return "", nil, fmt.Errorf("the first tracked file alone exceeds the %d byte budget; ask for a smaller slice", budget)
	}
	return b.String(), included, nil
}
