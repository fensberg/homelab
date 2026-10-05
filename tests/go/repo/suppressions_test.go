package repo

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// How each tool this repository runs can be silenced is written down once,
// in tests/silencers.yml, and nothing here knows a tool by name.
//
// The guard that reads every silencing and requires it to name the guard
// proving its reason is not here yet. It lands when the last silencing in the
// repository has one to name; until then it would be a rule with exceptions,
// which is the thing it exists to end. What is here is the half that can
// already hold: no tool arrives that the file has not heard of.

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
