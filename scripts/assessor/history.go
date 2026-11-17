package main

import (
	"fmt"
	"os/exec"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// history reads one revision of a git checkout.
type history struct{ dir, rev string }

// git runs one git command in the checkout and returns what it printed.
func (h history) git(args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", h.dir}, args...)...).Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// times parses one committer date to a line.
func times(lines string) ([]time.Time, error) {
	var out []time.Time
	for _, line := range strings.Fields(lines) {
		at, err := time.Parse(time.RFC3339, line)
		if err != nil {
			return nil, err
		}
		out = append(out, at)
	}
	return out, nil
}

// edits is when each commit that changed a file landed, oldest first.
func (h history) edits(file string) ([]time.Time, error) {
	out, err := h.git("log", "--format=%cI", "--reverse", h.rev, "--", file)
	if err != nil {
		return nil, err
	}
	return times(out)
}

var quoted = regexp.MustCompile(`"([^"]+)"`)

// requiredIn reads the list of required checks out of OpenTofu source: the
// quoted names between `required_checks = [` and its closing bracket,
// comments left out.
func requiredIn(source string) []string {
	var names []string
	inside := false
	for _, line := range strings.Split(source, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.Contains(trimmed, "required_checks = ["):
			inside = true
		case !inside || strings.HasPrefix(trimmed, "#"):
		case trimmed == "]":
			return names
		default:
			for _, m := range quoted.FindAllStringSubmatch(trimmed, -1) {
				names = append(names, m[1])
			}
		}
	}
	return names
}

// requirements is each set of required checks the repository has declared
// for itself, with the day it landed. Before the first, which checks were
// required is not on record.
func (h history) requirements() ([]requirement, error) {
	log, err := h.git("log", "--format=%H %cI", "--reverse", h.rev)
	if err != nil {
		return nil, err
	}
	var out []requirement
	for _, line := range strings.Split(strings.TrimSpace(log), "\n") {
		commit, date, _ := strings.Cut(line, " ")
		// git grep exits 1 when nothing matches, which is a commit from
		// before the list existed.
		source, _ := h.git("grep", "-h", "-A80", "-e", `required_checks = \[`, commit, "--", "*.tf")
		names := requiredIn(source)
		if len(names) == 0 || len(out) > 0 && slices.Equal(out[len(out)-1].Checks, names) {
			continue
		}
		at, err := time.Parse(time.RFC3339, date)
		if err != nil {
			return nil, err
		}
		out = append(out, requirement{From: at, Checks: names})
	}
	return out, nil
}

var (
	goGuard   = regexp.MustCompile(`(?m)((?:^//.*\n)+)func (Test\w+)\(`)
	issueRef  = regexp.MustCompile(`#(\d+)\b`)
	regoIntro = regexp.MustCompile(`\A((?:#.*\n)+)`)
)

// cited is every issue number a comment names, each once, in order.
func cited(comment string) []int {
	var out []int
	for _, m := range issueRef.FindAllStringSubmatch(comment, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// goGuardsIn is each test in Go source whose own comment cites an issue.
func goGuardsIn(file, source string) []guard {
	var out []guard
	for _, m := range goGuard.FindAllStringSubmatch(source, -1) {
		if issues := cited(m[1]); len(issues) > 0 {
			out = append(out, guard{Kind: "Go test", Name: m[2], File: file, Issues: issues})
		}
	}
	return out
}

// guards is every guard under guardsDir and every rule under rulesDir whose
// comment cites an issue, with the day it first landed under its present
// name.
func (h history) guards(guardsDir, rulesDir string) ([]guard, error) {
	files, err := h.git("ls-tree", "-r", "--name-only", h.rev, "--", guardsDir, rulesDir)
	if err != nil {
		return nil, err
	}
	var out []guard
	for _, file := range strings.Fields(files) {
		isGuard := strings.HasPrefix(file, guardsDir+"/") && strings.HasSuffix(file, "_test.go")
		isRule := strings.HasPrefix(file, rulesDir+"/") && strings.HasSuffix(file, ".rego") &&
			!strings.HasSuffix(file, "_test.rego")
		if !isGuard && !isRule {
			continue
		}
		source, err := h.git("show", h.rev+":"+file)
		if err != nil {
			return nil, err
		}
		var found []guard
		search := []string{"--diff-filter=A", h.rev, "--", file}
		if isGuard {
			found = goGuardsIn(file, source)
		} else if issues := cited(regoIntro.FindString(source)); len(issues) > 0 {
			found = []guard{{Kind: "Conftest rule", Name: path.Base(path.Dir(file)), File: file, Issues: issues}}
		}
		for _, g := range found {
			if isGuard {
				search = []string{"-Sfunc " + g.Name + "(", h.rev, "--", guardsDir}
			}
			log, err := h.git(append([]string{"log", "--format=%cI", "--reverse"}, search...)...)
			if err != nil {
				return nil, err
			}
			landed, err := times(log)
			if err != nil || len(landed) == 0 {
				return nil, fmt.Errorf("%s: no commit adds it: %v", g.Name, err)
			}
			g.Added = landed[0]
			out = append(out, g)
		}
	}
	return out, nil
}
