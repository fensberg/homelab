package repo

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"regexp"
	"slices"
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
	repository(t).YAMLInto(t, silencersFile, &s)
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
	// This package, in whichever tree is being judged: the mutation ledger
	// runs a guard against a scratch copy.
	tree := repository(t)
	guards := map[string]bool{}
	fset := token.NewFileSet()
	for _, rel := range tree.Files(t) {
		if filepath.Dir(rel) != guardsDir || !strings.HasSuffix(rel, "_test.go") {
			continue
		}
		for _, d := range tree.Go(t, fset, rel).Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "Test") {
				guards[fn.Name.Name] = true
			}
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
	// A reason may rest on more than one guard; `and` before a second name names the rest.
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

// commentLeaders is how a comment starts in a file of each kind, by the
// globs Semgrep picks the kind's files with. A mark only silences from inside
// a comment, and a sentence that mentions one is not a silencing. A patch is
// a change somebody has yet to apply: no tool reads it as the file it
// describes, so a mark inside it silences nothing until it is, and no kind
// here reads one.
var commentLeaders = []struct {
	globs   []string
	leaders []string
}{
	{[]string{"*.go", "*.js", "*.mjs", "*.cjs", "*.ts", "*.mts", "*.tsx", "*.jsx"}, []string{"//", "/*"}},
	{[]string{"*.md", "*.html"}, []string{"<!--"}},
	{[]string{"*.tf", "*.hcl"}, []string{"#", "//"}},
	{nil, []string{"#"}}, // everything else
}

// commentLines is what a scan finds of a file's comments: each line that is
// a comment and nothing else, and each line whose comment - from the line's
// first leader - carries one of the marks, by line number.
type commentLines struct {
	whole, marked map[int]string
}

// leadersOf is how a comment starts in the file at rel, by its kind.
func leadersOf(rel string) []string {
	base := filepath.Base(rel)
	for _, kind := range commentLeaders {
		for _, glob := range kind.globs {
			if ok, _ := filepath.Match(glob, base); ok {
				return kind.leaders
			}
		}
	}
	if ok, _ := filepath.Match("*.patch", base); ok {
		return nil
	}
	if ok, _ := filepath.Match("*.diff", base); ok {
		return nil
	}
	return commentLeaders[len(commentLeaders)-1].leaders
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

// commentScan asks Semgrep for the comments of files that carry a mark, in
// two passes so that neither reads more than it must: every line holding a
// mark, anywhere, which is few; and then the whole-line comments of only the
// files one was found in, for the run of comment around each.
func commentScan(t *testing.T, files []string, marks []string) map[string]*commentLines {
	t.Helper()
	tree := repository(t)
	var quoted []string
	for _, m := range marks {
		quoted = append(quoted, regexp.QuoteMeta(m))
	}
	rule := func(id, pattern string) map[string]any {
		return map[string]any{
			"id": id, "languages": []string{"generic"}, "severity": "INFO",
			"message": "$C", "metadata": map[string]any{"about": id}, "pattern-regex": pattern,
		}
	}
	out := map[string]*commentLines{}
	var marked []string
	for _, f := range scanText(t, tree, []map[string]any{rule("marked", `(?m)^(?P<C>[^\n]*(?:`+strings.Join(quoted, "|")+`)[^\n]*)$`)}, files) {
		comment := commentOn(f.Text, leadersOf(f.Path))
		if !slices.ContainsFunc(marks, func(m string) bool { return strings.Contains(comment, m) }) {
			continue // a mark outside a comment silences nothing
		}
		if out[f.Path] == nil {
			out[f.Path] = &commentLines{whole: map[int]string{}, marked: map[int]string{}}
			marked = append(marked, f.Path)
		}
		out[f.Path].marked[f.Line] = comment
	}
	if len(marked) == 0 {
		return out
	}
	for _, f := range scanText(t, tree, []map[string]any{rule("whole", `(?m)^[ \t]*(?P<C>(?://|/\*|<!--|#)[^\n]*?)[ \t]*$`)}, marked) {
		if c := out[f.Path]; c != nil && slices.ContainsFunc(leadersOf(f.Path), func(l string) bool { return strings.HasPrefix(f.Text, l) }) {
			c.whole[f.Line] = f.Text
		}
	}
	return out
}

// around is the comment a mark sits in: the comment on its own line, and the
// unbroken run of comment-only lines above and below it - below only when
// the mark is itself in a comment-only line, since a mark at the end of a
// line of code has that code under it, not its own comment.
func (c *commentLines) around(line int, comment string) string {
	block := []string{comment}
	for j := line - 1; c.whole[j] != ""; j-- {
		block = append(block, c.whole[j])
	}
	if c.whole[line] != "" {
		for j := line + 1; c.whole[j] != ""; j++ {
			block = append(block, c.whole[j])
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
	var files []string
	for _, rel := range trackedFiles(t) {
		// The one file that spells every mark, to say what they are.
		if rel != silencersFile {
			files = append(files, rel)
		}
	}
	read, found := len(files), 0
	var texts []string
	for _, m := range marks {
		texts = append(texts, m.text)
	}
	for rel, comments := range commentScan(t, files, texts) {
		for line, comment := range comments.marked {
			for _, m := range marks {
				if !strings.Contains(comment, m.text) {
					continue
				}
				found++
				if why := unproved(comments.around(line, comment), guards); why != "" {
					failures = append(failures, rel+":"+strconv.Itoa(line)+" silences "+m.tool+" ("+m.text+") and "+why)
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
	var lineFiles []string
	for _, def := range s.Tools {
		for _, c := range def.Config {
			if len(c.Lists)+len(c.Maps) == 0 {
				lineFiles = append(lineFiles, c.File)
			}
		}
	}
	lines := configLines(t, lineFiles)
	for tool, def := range s.Tools {
		for _, c := range def.Config {
			switch {
			case len(c.Lists)+len(c.Maps) > 0:
				doc := repository(t).YAMLNode(t, c.File)
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
				file := lines[c.File]
				if file == nil {
					file = &commentLines{whole: map[int]string{}, marked: map[int]string{}}
				}
				for i, trimmed := range file.marked {
					entry := c.Lines
					for _, k := range c.Keys {
						entry = entry || regexp.MustCompile(`^`+regexp.QuoteMeta(k)+`\s*=`).MatchString(trimmed)
					}
					for _, table := range c.Tables {
						entry = entry || trimmed == "[["+table+"]]"
					}
					if entry {
						inline := ""
						if at := strings.Index(trimmed, "#"); at >= 0 {
							inline = trimmed[at:]
						}
						out = append(out, configEntry{tool, c.File + ":" + strconv.Itoa(i), trimmed, file.around(i, inline)})
					}
				}
			}
		}
	}
	return out
}

// configLines is what Semgrep finds in configuration files read line by
// line: each comment line, and each line that is an entry - anything else
// that is not blank - kept, for the entries, as marked.
func configLines(t *testing.T, files []string) map[string]*commentLines {
	t.Helper()
	rule := func(id, pattern string) map[string]any {
		return map[string]any{
			"id": id, "languages": []string{"generic"}, "severity": "INFO",
			"message": "$C", "metadata": map[string]any{"about": id}, "pattern-regex": pattern,
		}
	}
	out := map[string]*commentLines{}
	for _, f := range scanText(t, repository(t), []map[string]any{
		rule("whole", `(?m)^[ \t]*(?P<C>#[^\n]*?)[ \t]*$`),
		rule("marked", `(?m)^[ \t]*(?P<C>[^#\s][^\n]*?)[ \t]*$`),
	}, files) {
		c := out[f.Path]
		if c == nil {
			c = &commentLines{whole: map[int]string{}, marked: map[int]string{}}
			out[f.Path] = c
		}
		if f.About == "whole" {
			c.whole[f.Line] = f.Text
		} else {
			c.marked[f.Line] = f.Text
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
	for name := range versionsPinned(t) {
		if regexp.MustCompile(`^[A-Z][A-Z0-9_]*_VERSION$`).MatchString(name) {
			arrives[name] = versionPins
		}
	}
	for _, repo := range parsePreCommitConfig(t).Repos {
		for _, hook := range repo.Hooks {
			arrives[hook.ID] = ".pre-commit-config.yaml"
		}
	}
	uses := regexp.MustCompile(`^([A-Za-z0-9_.-]+/[A-Za-z0-9_./-]+)@`)
	image := regexp.MustCompile(`^([a-z0-9./-]+):`)
	for _, rel := range trackedFiles(t) {
		if !strings.HasPrefix(rel, ".github/") || !(strings.HasSuffix(rel, ".yml") || strings.HasSuffix(rel, ".yaml")) {
			continue
		}
		docs := repository(t).YAMLDocuments(t, rel)
		for _, v := range scalarsAt(docs, "uses") {
			if m := uses.FindStringSubmatch(v); m != nil {
				arrives[m[1]] = rel
			}
		}
		for _, v := range scalarsAt(docs, "image") {
			if m := image.FindStringSubmatch(v); m != nil {
				arrives[m[1]] = rel
			}
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
