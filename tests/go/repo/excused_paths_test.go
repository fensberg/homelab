package repo

import (
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A path a tool is told not to read holds nothing this repository wrote to be
// read by it.
//
// Excluding a path is the widest way to silence a tool: every finding under
// it, present and future, for every rule. Each exclusion here gives a reason,
// and the reasons come down to five things that can be read off the
// repository, so this reads them. Every file an exclusion covers is one of:
//
//   - not tracked. Ignored by git, so absent from every clone and every scan
//     in CI, and excluded only so a run on a workstation agrees with the lane.
//   - a lockfile, in the form its package manager writes, with no line a
//     person put there.
//   - vendored: somebody else's file, committed as its publisher shipped it,
//     and marked so in .gitattributes. Other guards hold it to the version
//     pinned; an exclusion cites those beside this.
//   - a kustomization that only assembles other files, each still read as the
//     file it is.
//   - test material: under a tests, fixtures or testdata directory.
//
// A file that is none of these is the repository's own, and the exclusion is
// wider than its reason. That is not hypothetical: the first run of this found
// a README and a values file of ours inside a directory excused as a
// vendored chart's output.
//
// Finds each exclusion by what tests/silencers.yml says a tool's
// configuration holds, and each tool's own way of writing a path.

// excludedPaths is every path a tool's configuration excludes, with how to
// match it against a tracked file.
type excludedPath struct {
	where, pattern string
	covers         func(path string) bool
}

func regexPath(pattern string) func(string) bool {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return func(string) bool { return true }
	}
	return re.MatchString
}

// globPath matches the way ignore files and globs do: a pattern with no
// slash names a file or directory at any depth, and one with slashes is
// anchored, with ** and a leading */ standing for any directories.
func globPath(pattern string) func(string) bool {
	p := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(pattern, "./"), "**/"), "/**")
	anyDepth := !strings.Contains(strings.TrimPrefix(pattern, "./"), "/") || strings.HasPrefix(pattern, "**/") || strings.HasPrefix(p, "*/")
	p = strings.TrimPrefix(p, "*/")
	return func(path string) bool {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
		return anyDepth && (strings.HasSuffix(path, "/"+p) || strings.Contains(path, "/"+p+"/"))
	}
}

func excludedPaths(t *testing.T) []excludedPath {
	t.Helper()
	var out []excludedPath
	add := func(file string, line int, pattern string, covers func(string) bool) {
		out = append(out, excludedPath{file + ":" + strconv.Itoa(line), pattern, covers})
	}

	// Checkov: skip-path, each a regular expression searched for in a path.
	var checkov struct {
		SkipPath []yaml.Node `yaml:"skip-path"`
	}
	if err := yaml.Unmarshal([]byte(readRepoFile(t, ".checkov.yaml")), &checkov); err != nil {
		t.Fatalf(".checkov.yaml does not parse: %v", err)
	}
	for _, n := range checkov.SkipPath {
		add(".checkov.yaml", n.Line, n.Value, regexPath(n.Value))
	}

	// markdownlint: ignores, each a glob.
	var markdownlint struct {
		Ignores []yaml.Node `yaml:"ignores"`
	}
	if err := yaml.Unmarshal([]byte(readRepoFile(t, ".markdownlint-cli2.yaml")), &markdownlint); err != nil {
		t.Fatalf(".markdownlint-cli2.yaml does not parse: %v", err)
	}
	for _, n := range markdownlint.Ignores {
		add(".markdownlint-cli2.yaml", n.Line, n.Value, globPath(n.Value))
	}

	for i, line := range strings.Split(readRepoFile(t, ".codespellrc"), "\n") {
		// codespell: skip, a comma-separated list of globs.
		if list, ok := strings.CutPrefix(strings.TrimSpace(line), "skip ="); ok {
			for _, p := range strings.Split(list, ",") {
				add(".codespellrc", i+1, strings.TrimSpace(p), globPath(strings.TrimSpace(p)))
			}
		}
	}
	for i, line := range strings.Split(readRepoFile(t, ".prettierignore"), "\n") {
		// prettier: an ignore file, a glob to a line.
		if p := strings.TrimSpace(line); p != "" && !strings.HasPrefix(p, "#") {
			add(".prettierignore", i+1, p, globPath(p))
		}
	}
	for i, line := range strings.Split(readRepoFile(t, ".gitleaks.toml"), "\n") {
		// gitleaks: an allowlist's paths, each a regular expression.
		if list, ok := strings.CutPrefix(strings.TrimSpace(line), "paths ="); ok {
			for _, m := range regexp.MustCompile(`'''(.*?)'''`).FindAllStringSubmatch(list, -1) {
				add(".gitleaks.toml", i+1, m[1], regexPath(m[1]))
			}
		}
	}
	if len(out) < 10 {
		t.Fatalf("found %d excluded path(s), which is fewer than the tools' configurations hold: a reader here has stopped matching", len(out))
	}
	return out
}

// vendoredFiles is the tracked files .gitattributes marks as somebody else's.
func vendoredFiles(t *testing.T, files []string) map[string]bool {
	t.Helper()
	cmd := exec.Command("git", "-C", repoRoot(t), "check-attr", "-z", "--stdin", "linguist-vendored")
	cmd.Stdin = strings.NewReader(strings.Join(files, "\x00") + "\x00")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("asking git which files are marked vendored: %v", err)
	}
	vendored := map[string]bool{}
	fields := strings.Split(string(out), "\x00")
	for i := 0; i+2 < len(fields); i += 3 {
		if fields[i+2] == "set" || fields[i+2] == "true" {
			vendored[fields[i]] = true
		}
	}
	return vendored
}

var (
	goSumLine    = regexp.MustCompile(`^\S+ v\S+ h1:[A-Za-z0-9+/]+=*$`)
	testMaterial = regexp.MustCompile(`(^|/)(tests|fixtures|testdata)/`)
)

// whyOurs says what makes a tracked file the repository's own writing, or ""
// when it is a lockfile, vendored, an assembling kustomization or test
// material.
func whyOurs(t *testing.T, path string, vendored map[string]bool) string {
	t.Helper()
	switch {
	case vendored[path], testMaterial.MatchString(path):
		return ""
	case filepath.Base(path) == "go.sum":
		for _, line := range strings.Split(strings.TrimSpace(readRepoFile(t, path)), "\n") {
			if !goSumLine.MatchString(line) {
				return "is a go.sum with a line Go did not write"
			}
		}
		return ""
	case filepath.Base(path) == "pnpm-lock.yaml":
		body := readRepoFile(t, path)
		if !strings.HasPrefix(body, "lockfileVersion:") || regexp.MustCompile(`(?m)^\s*#`).MatchString(body) {
			return "is a pnpm lockfile with something pnpm did not write"
		}
		return ""
	case filepath.Base(path) == "kustomization.yaml":
		dec := yaml.NewDecoder(strings.NewReader(readRepoFile(t, path)))
		for {
			var doc struct {
				Kind string `yaml:"kind"`
			}
			err := dec.Decode(&doc)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return "does not parse as YAML, so what it holds is not known"
			}
			if doc.Kind != "Kustomization" {
				return "holds a " + doc.Kind + ", and only a file that assembles others may go unread"
			}
		}
		return ""
	}
	return "is this repository's own, and is not a lockfile, vendored, a kustomization or test material"
}

func TestAnExcludedPathHoldsNothingTheToolWasMeantToRead(t *testing.T) {
	tracked := trackedFiles(t)
	vendored := vendoredFiles(t, tracked)

	var failures []string
	for _, ex := range excludedPaths(t) {
		for _, path := range tracked {
			if !ex.covers(path) {
				continue
			}
			if why := whyOurs(t, path, vendored); why != "" {
				failures = append(failures, ex.where+" excludes "+ex.pattern+", which covers "+path+"\n      "+path+" "+why)
			}
		}
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		if len(failures) > 25 {
			failures = append(failures[:25], "... and more")
		}
		t.Errorf("a tool is told not to read files it was meant to:\n\n  %s\n\n"+
			"Narrow the exclusion to what its reason covers. A file somebody else published is "+
			"marked linguist-vendored in .gitattributes; anything else under an excluded path is "+
			"read like the rest of the repository.", strings.Join(failures, "\n  "))
	}
}

// A word codespell is told to accept is only ever written as code.
//
// The one reason to accept a word a spell checker rejects is that it is not
// prose: a flag, a command, an identifier, spelt as its owner spells it. So
// that is what is held. In a document every use of such a word is inside a
// code block or a code span, where it is plainly a quotation of something a
// machine reads, and it is used at all - an accepted word nobody writes is an
// exemption waiting to hide a real misspelling.
func TestAWordCodespellAcceptsIsOnlyEverWrittenAsCode(t *testing.T) {
	var words []string
	for _, line := range strings.Split(readRepoFile(t, ".codespellrc"), "\n") {
		if list, ok := strings.CutPrefix(strings.TrimSpace(line), "ignore-words-list ="); ok {
			for _, w := range strings.Split(list, ",") {
				if w = strings.TrimSpace(w); w != "" {
					words = append(words, w)
				}
			}
		}
	}
	// Told to accept nothing, there is nothing to hold it to.
	if len(words) == 0 {
		return
	}

	fence := regexp.MustCompile("^\\s*(```|~~~)")
	span := regexp.MustCompile("`[^`]*`")
	for _, word := range words {
		used := regexp.MustCompile(`\b` + regexp.QuoteMeta(word) + `\b`)
		uses := 0
		for _, rel := range trackedFiles(t) {
			if rel == ".codespellrc" || !strings.HasSuffix(rel, ".md") {
				continue
			}
			fenced := false
			for i, line := range strings.Split(readRepoFile(t, rel), "\n") {
				if fence.MatchString(line) {
					fenced = !fenced
					continue
				}
				if !used.MatchString(line) {
					continue
				}
				uses++
				indented := strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t")
				if fenced || indented || !used.MatchString(span.ReplaceAllString(line, "")) {
					continue
				}
				t.Errorf("%s:%d writes %q in a sentence. codespell is told to accept it as a name a machine reads, "+
					"and here it is prose: put it in a code span, or it is a misspelling nothing will catch.", rel, i+1, word)
			}
		}
		if uses == 0 {
			t.Errorf("codespell is told to accept %q, and no document writes it. Take it out of ignore-words-list: "+
				"an accepted word nobody uses only hides the day somebody misspells into it.", word)
		}
	}
}
