package repo

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A finding that is silenced names the guard that proves it may be.
//
// WHY THIS EXISTS. A playbook task asked the hypervisor's exporter a question
// over plain HTTP, the policy scan objected, and the objection was silenced
// with a sentence saying why that was fine. The sentence was plausible and
// nothing checked it. What it excused was an exporter serving the host's
// facts in the clear, and the right answer was to serve them over TLS, which
// the estate already had everything needed to do
// (docs/epochs/04-observability.md).
//
// A reason in a comment is an assertion. This repository does not take a
// declaration on trust anywhere else, and a silenced finding is the one place
// it still did: fifty-odd of them, across eight tools, with no guard over any.
// So the reason is now a test. Every silencing carries the words `proved by`
// and the name of a guard in this package, and the guard is what makes the
// reason true: "fetched by digest, not from git" has one, and "plain HTTP is
// fine here" could not.
//
// tests/silencers.yml says how each tool is silenced. Nothing here knows a
// tool by name.

// silencers is tests/silencers.yml.
type silencers struct {
	Tools map[string]struct {
		ArrivesAs []string `yaml:"arrives_as"`
		Marks     []string `yaml:"marks"`
		Config    []struct {
			File   string   `yaml:"file"`
			Lists  []string `yaml:"lists"`
			Maps   []string `yaml:"maps"`
			Keys   []string `yaml:"keys"`
			Tables []string `yaml:"tables"`
			Lines  bool     `yaml:"lines"`
		} `yaml:"config"`
	} `yaml:"tools"`
	JudgesNothing map[string]string `yaml:"judges_nothing"`
}

const silencersFile = "tests/silencers.yml"

func readSilencers(t *testing.T) silencers {
	t.Helper()
	var s silencers
	if err := yaml.Unmarshal([]byte(readRepoFile(t, silencersFile)), &s); err != nil {
		t.Fatalf("%s does not parse: %v", silencersFile, err)
	}
	if len(s.Tools) == 0 {
		t.Fatalf("%s names no tools, so every check that reads it is asserting nothing", silencersFile)
	}
	return s
}

// provedBy is how a silencing names its guard.
var provedBy = regexp.MustCompile(`proved by (Test[A-Za-z0-9_]+)`)

// guardsHere is every test this package declares, by name. The package is
// the directory a test runs in.
func guardsHere(t *testing.T) map[string]bool {
	t.Helper()
	here, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(here, "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	decl := regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]+)\(`)
	guards := map[string]bool{}
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("reading %s: %v", file, err)
		}
		for _, m := range decl.FindAllStringSubmatch(string(body), -1) {
			guards[m[1]] = true
		}
	}
	if len(guards) == 0 {
		t.Fatal("found no guards in this package, so no silencing could name one")
	}
	return guards
}

// unproved says what is wrong with the reason a silencing gives, or "" when
// it names a guard that exists.
func unproved(comment string, guards map[string]bool) string {
	cited := provedBy.FindAllStringSubmatch(comment, -1)
	if len(cited) == 0 {
		return "names no guard"
	}
	// A reason may rest on more than one guard; `and TestOther` names the rest.
	for _, more := range regexp.MustCompile(`\band (Test[A-Za-z0-9_]+)`).FindAllStringSubmatch(comment, -1) {
		cited = append(cited, more)
	}
	for _, m := range cited {
		if !guards[m[1]] {
			return "names " + m[1] + ", and there is no guard of that name"
		}
	}
	return ""
}

// commentLeaders is how a comment starts in a file of this name. A mark only
// silences from inside a comment, and a sentence that mentions one is not a
// silencing.
func commentLeaders(rel string) []string {
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".go", ".js", ".mjs", ".cjs", ".ts", ".mts", ".tsx", ".jsx":
		return []string{"//", "/*"}
	case ".md", ".html":
		return []string{"<!--"}
	case ".tf", ".hcl":
		return []string{"#", "//"}
	}
	return []string{"#"}
}

// commentOn is the comment a line carries, from its first leader, or "".
func commentOn(line string, leaders []string) string {
	at := -1
	for _, l := range leaders {
		if i := strings.Index(line, l); i >= 0 && (at < 0 || i < at) {
			at = i
		}
	}
	if at < 0 {
		return ""
	}
	return line[at:]
}

// commentAround is the comment a mark sits in: the comment on its own line,
// and the unbroken run of comment-only lines above and below it.
func commentAround(lines []string, i int, leaders []string) string {
	block := []string{commentOn(lines[i], leaders)}
	whole := func(j int) (string, bool) {
		trimmed := strings.TrimSpace(lines[j])
		c := commentOn(trimmed, leaders)
		return c, c != "" && c == trimmed
	}
	for j := i - 1; j >= 0; j-- {
		c, ok := whole(j)
		if !ok {
			break
		}
		block = append(block, c)
	}
	// Below only when the mark is itself in a comment-only line: a mark at
	// the end of a line of code has that code under it, not its own comment.
	if _, ok := whole(i); ok {
		for j := i + 1; j < len(lines); j++ {
			c, ok := whole(j)
			if !ok {
				break
			}
			block = append(block, c)
		}
	}
	return strings.Join(block, "\n")
}

func TestEverySilencedFindingNamesTheGuardThatProvesItMayBe(t *testing.T) {
	s := readSilencers(t)
	guards := guardsHere(t)

	type mark struct{ tool, text string }
	var marks []mark
	for tool, def := range s.Tools {
		for _, m := range def.Marks {
			marks = append(marks, mark{tool, m})
		}
	}
	if len(marks) == 0 {
		t.Fatalf("%s declares no marks, so this found nothing because it looked for nothing", silencersFile)
	}

	var failures []string
	read, found := 0, 0
	for _, rel := range trackedFiles(t) {
		// The one file that spells every mark, to say what they are.
		if rel == silencersFile {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		// Not text, so it holds no comment.
		if bytes.IndexByte(raw, 0) >= 0 {
			continue
		}
		read++
		leaders := commentLeaders(rel)
		lines := strings.Split(string(raw), "\n")
		for i, line := range lines {
			comment := commentOn(line, leaders)
			if comment == "" {
				continue
			}
			for _, m := range marks {
				if !strings.Contains(comment, m.text) {
					continue
				}
				found++
				if why := unproved(commentAround(lines, i, leaders), guards); why != "" {
					failures = append(failures, rel+":"+strconv.Itoa(i+1)+" silences "+m.tool+" ("+m.text+") and "+why)
				}
				break
			}
		}
	}
	if read == 0 {
		t.Fatal("read no files, so no silencing was looked for")
	}
	t.Logf("%d silenced finding(s) in %d file(s)", found, read)

	for _, f := range s.configEntries(t) {
		if why := unproved(f.comment, guards); why != "" {
			failures = append(failures, f.where+" excuses "+f.what+" from "+f.tool+" and "+why)
		}
	}

	if len(failures) > 0 {
		sort.Strings(failures)
		t.Errorf("%d silenced finding(s) give a reason nothing proves:\n\n  %s\n\n"+
			"A silencing says the tool is wrong here, and a sentence saying why is not checked by "+
			"anybody. Write `proved by <TestName>` in its comment, naming the guard in this package "+
			"that makes the reason true. If no guard could, the finding is right: fix what it found.",
			len(failures), strings.Join(failures, "\n  "))
	}
}

// configEntry is one thing a tool's own configuration excuses.
type configEntry struct {
	tool, where, what, comment string
}

// configEntries reads every tool's configuration for what it excuses, with
// the comment above each entry.
func (s silencers) configEntries(t *testing.T) []configEntry {
	t.Helper()
	var out []configEntry
	for tool, def := range s.Tools {
		for _, c := range def.Config {
			body := readRepoFile(t, c.File)
			switch {
			case len(c.Lists)+len(c.Maps) > 0:
				var doc yaml.Node
				if err := yaml.Unmarshal([]byte(body), &doc); err != nil {
					t.Fatalf("%s does not parse: %v", c.File, err)
				}
				if len(doc.Content) == 0 {
					continue
				}
				for _, path := range c.Lists {
					for _, n := range nodesAt(doc.Content[0], strings.Split(path, ".")) {
						if n.Kind != yaml.SequenceNode {
							continue
						}
						for _, item := range n.Content {
							out = append(out, configEntry{tool, c.File + ":" + strconv.Itoa(item.Line), item.Value, item.HeadComment + "\n" + item.LineComment})
						}
					}
				}
				for _, path := range c.Maps {
					for _, n := range nodesAt(doc.Content[0], strings.Split(path, ".")) {
						if n.Kind != yaml.MappingNode {
							continue
						}
						for i := 0; i+1 < len(n.Content); i += 2 {
							// A rule switched off. One given settings is
							// still on, and is not a silencing.
							if v := n.Content[i+1]; v.Kind != yaml.ScalarNode || v.Value != "false" {
								continue
							}
							k := n.Content[i]
							out = append(out, configEntry{tool, c.File + ":" + strconv.Itoa(k.Line), k.Value, k.HeadComment + "\n" + k.LineComment + "\n" + n.Content[i+1].LineComment})
						}
					}
				}
			default:
				lines := strings.Split(body, "\n")
				for i, line := range lines {
					trimmed := strings.TrimSpace(line)
					if trimmed == "" || strings.HasPrefix(trimmed, "#") {
						continue
					}
					entry := c.Lines
					for _, k := range c.Keys {
						entry = entry || regexp.MustCompile(`^`+regexp.QuoteMeta(k)+`\s*=`).MatchString(trimmed)
					}
					for _, table := range c.Tables {
						entry = entry || trimmed == "[["+table+"]]"
					}
					if entry {
						out = append(out, configEntry{tool, c.File + ":" + strconv.Itoa(i+1), trimmed, commentAround(lines, i, []string{"#"})})
					}
				}
			}
		}
	}
	return out
}

// nodesAt walks a YAML mapping by keys, where "*" is every key.
func nodesAt(n *yaml.Node, path []string) []*yaml.Node {
	if len(path) == 0 {
		return []*yaml.Node{n}
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	var out []*yaml.Node
	for i := 0; i+1 < len(n.Content); i += 2 {
		if path[0] == "*" || n.Content[i].Value == path[0] {
			out = append(out, nodesAt(n.Content[i+1], path[1:])...)
		}
	}
	return out
}

// A tool this repository runs is one tests/silencers.yml has heard of.
//
// The guard above finds a silencing by the marks that file declares, so a
// scanner added tomorrow, with a mark of its own, would be silenced where
// nothing looks. Every way a tool arrives is enumerated here - pinned in
// scripts/versions.env, run as a pre-commit hook, used as an action, run as an
// image in a workflow - and each is either a tool with the ways it is
// silenced, or declared to judge nothing. And the reverse: an entry for a
// tool that no longer arrives is a claim about nothing.
func TestEveryToolThatArrivesSaysHowItIsSilenced(t *testing.T) {
	s := readSilencers(t)

	known := map[string]string{}
	for tool, def := range s.Tools {
		for _, a := range def.ArrivesAs {
			known[a] = tool
		}
	}
	for a := range s.JudgesNothing {
		if tool, both := known[a]; both {
			t.Errorf("%s lists %s under %s and under judges_nothing. It is one or the other.", silencersFile, a, tool)
		}
		known[a] = "judges_nothing"
	}

	arrives := map[string]string{}
	for _, m := range regexp.MustCompile(`(?m)^([A-Z][A-Z0-9_]*_VERSION)=`).FindAllStringSubmatch(readRepoFile(t, "scripts/versions.env"), -1) {
		arrives[m[1]] = "scripts/versions.env"
	}
	for _, m := range regexp.MustCompile(`(?m)^\s*- id:\s*([a-z0-9-]+)\s*$`).FindAllStringSubmatch(readRepoFile(t, ".pre-commit-config.yaml"), -1) {
		arrives[m[1]] = ".pre-commit-config.yaml"
	}
	uses := regexp.MustCompile(`(?m)^\s*(?:- )?uses:\s*["']?([A-Za-z0-9_.-]+/[A-Za-z0-9_./-]+)@`)
	image := regexp.MustCompile(`(?m)^\s*image:\s*["']?([a-z0-9./-]+):`)
	for _, rel := range trackedFiles(t) {
		if !strings.HasPrefix(rel, ".github/") || !(strings.HasSuffix(rel, ".yml") || strings.HasSuffix(rel, ".yaml")) {
			continue
		}
		body := stripYAMLComments(readRepoFile(t, rel))
		for _, m := range uses.FindAllStringSubmatch(body, -1) {
			arrives[m[1]] = rel
		}
		for _, m := range image.FindAllStringSubmatch(body, -1) {
			arrives[m[1]] = rel
		}
	}
	if len(arrives) < 10 {
		t.Fatalf("found %d tool(s) arriving, which is too few to be the repository's: the patterns here have stopped matching", len(arrives))
	}

	var unheard, gone []string
	for a, from := range arrives {
		if _, ok := known[a]; !ok {
			unheard = append(unheard, a+" (from "+from+")")
		}
	}
	for a, tool := range known {
		if _, ok := arrives[a]; !ok {
			gone = append(gone, a+" (listed under "+tool+")")
		}
	}
	sort.Strings(unheard)
	sort.Strings(gone)
	if len(unheard) > 0 {
		t.Errorf("%d tool(s) arrive that %s has not heard of:\n\n  %s\n\n"+
			"Add each under tools: with the marks and configuration that silence it, or under "+
			"judges_nothing: with what it does instead. Until then a finding of its could be "+
			"silenced where no guard looks.",
			len(unheard), silencersFile, strings.Join(unheard, "\n  "))
	}
	if len(gone) > 0 {
		t.Errorf("%s names %d tool(s) that no longer arrive:\n\n  %s\n\nRemove them with the tool.",
			silencersFile, len(gone), strings.Join(gone, "\n  "))
	}
}
