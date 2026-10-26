package repo

import (
	"homelab/tests/parsed"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"homelab/details/applications"
	"homelab/details/flux"
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
	tree := repository(t)
	add := func(file string, line int, pattern string, covers func(string) bool) {
		where := file
		if line > 0 {
			where += ":" + strconv.Itoa(line)
		}
		out = append(out, excludedPath{where, pattern, covers})
	}

	// Checkov: skip-path, each a regular expression searched for in a path.
	var checkov struct {
		SkipPath []yaml.Node `yaml:"skip-path"`
	}
	if err := tree.YAMLNode(t, ".checkov.yaml").Decode(&checkov); err != nil {
		t.Fatalf(".checkov.yaml is not the shape this reads: %v", err)
	}
	for _, n := range checkov.SkipPath {
		add(".checkov.yaml", n.Line, n.Value, regexPath(n.Value))
	}

	// markdownlint: ignores, each a glob.
	var markdownlint struct {
		Ignores []yaml.Node `yaml:"ignores"`
	}
	if err := tree.YAMLNode(t, ".markdownlint-cli2.yaml").Decode(&markdownlint); err != nil {
		t.Fatalf(".markdownlint-cli2.yaml is not the shape this reads: %v", err)
	}
	for _, n := range markdownlint.Ignores {
		add(".markdownlint-cli2.yaml", n.Line, n.Value, globPath(n.Value))
	}

	// codespell: skip, a comma-separated list of globs.
	for _, p := range codespellList(t, "skip") {
		add(".codespellrc", 0, p, globPath(p))
	}
	// prettier: an ignore file, a glob to a line.
	entries, _ := tree.Conftest(t, ".prettierignore", "ignore").([]any)
	for i, e := range entries {
		if entry, _ := e.(map[string]any); entry["Kind"] == "Path" {
			p, _ := entry["Value"].(string)
			add(".prettierignore", i+1, p, globPath(p))
		}
	}
	// gitleaks: an allowlist's paths, each a regular expression.
	var gitleaks struct {
		Allowlists []struct {
			Paths []string `json:"paths"`
		} `json:"allowlists"`
	}
	if err := as(tree.Conftest(t, ".gitleaks.toml", "toml"), &gitleaks); err != nil {
		t.Fatalf(".gitleaks.toml is not the shape this reads: %v", err)
	}
	for _, list := range gitleaks.Allowlists {
		for _, p := range list.Paths {
			add(".gitleaks.toml", 0, p, regexPath(p))
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
	out, _, err := repository(t).Invoke(parsed.Run{Stdin: strings.Join(files, "\x00") + "\x00"}, "git", "check-attr", "-z", "--stdin", "linguist-vendored")
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

var testMaterial = regexp.MustCompile(`(^|/)(tests|fixtures|testdata)/`)

// holdsAComment reports whether any node of a YAML tree carries a comment.
func holdsAComment(n *yaml.Node) bool {
	if n.HeadComment != "" || n.LineComment != "" || n.FootComment != "" {
		return true
	}
	for _, c := range n.Content {
		if holdsAComment(c) {
			return true
		}
	}
	return false
}

// codespellList is a comma-separated setting of .codespellrc, as Conftest's
// INI parser reads the file.
func codespellList(t *testing.T, key string) []string {
	t.Helper()
	ini, _ := repository(t).Conftest(t, ".codespellrc", "ini").(map[string]any)
	section, _ := ini["codespell"].(map[string]any)
	list, _ := section[key].(string)
	var out []string
	for _, v := range strings.Split(list, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// whyOurs says what makes a tracked file the repository's own writing, or ""
// when it is a lockfile, vendored, an assembling kustomization or test
// material.
func whyOurs(t *testing.T, path string, vendored map[string]bool) string {
	t.Helper()
	switch {
	case vendored[path], testMaterial.MatchString(path):
		return ""
	case filepath.Base(path) == "go.sum":
		if _, err := repository(t).GoSum(t, path); err != nil {
			return "is a go.sum with a line Go did not write"
		}
		return ""
	case filepath.Base(path) == "pnpm-lock.yaml":
		lock := repository(t).YAMLNode(t, path)
		if len(lock.Content) == 0 || len(lock.Content[0].Content) == 0 || lock.Content[0].Content[0].Value != "lockfileVersion" || holdsAComment(lock) {
			return "is a pnpm lockfile with something pnpm did not write"
		}
		return ""
	case filepath.Base(path) == applications.Kustomization:
		docs, isYAML := repository(t).MaybeYAML(t, path)
		if !isYAML {
			return "does not parse as YAML, so what it holds is not known"
		}
		for _, d := range docs {
			var doc struct {
				Kind string `yaml:"kind"`
			}
			if as(d, &doc) != nil || doc.Kind != flux.Kustomization {
				return "holds a " + doc.Kind + ", and only a file that assembles others may go unread"
			}
		}
		return ""
	}
	return "is this repository's own, and is not a lockfile, vendored, a kustomization or test material"
}

func TestAnExcludedPathHoldsNothingTheToolWasMeantToRead(t *testing.T) {
	light(t)
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
	light(t)
	words := codespellList(t, "ignore-words-list")
	// Told to accept nothing, there is nothing to hold it to.
	if len(words) == 0 {
		return
	}

	tree := repository(t)
	documents := map[string][]parsed.MarkdownPart{}
	for _, rel := range trackedFiles(t) {
		if rel != ".codespellrc" && strings.HasSuffix(rel, ".md") {
			documents[rel] = tree.Markdown(t, rel)
		}
	}
	for _, word := range words {
		used := regexp.MustCompile(`\b` + regexp.QuoteMeta(word) + `\b`)
		uses := 0
		for rel, parts := range documents {
			for _, part := range parts {
				if !used.MatchString(part.Text) {
					continue
				}
				uses++
				if part.Kind != parsed.MarkdownText {
					continue // a code block, an indented one, a code span: a quotation
				}
				t.Errorf("%s:%d writes %q in a sentence. codespell is told to accept it as a name a machine reads, "+
					"and here it is prose: put it in a code span, or it is a misspelling nothing will catch.", rel, part.Line, word)
			}
		}
		if uses == 0 {
			t.Errorf("codespell is told to accept %q, and no document writes it. Take it out of ignore-words-list: "+
				"an accepted word nobody uses only hides the day somebody misspells into it.", word)
		}
	}
}
