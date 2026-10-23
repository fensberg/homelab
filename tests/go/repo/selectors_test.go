package repo

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"homelab/tests/parsed"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The places a configuration rule reads, proved one by one.
//
// A rule reads its files only through the selectors in lib
// (TestOnlyTheSelectorsReadTheInput). So the places it reads are named: the
// selectors it uses, and what it gives the ones that take a path. Its own
// tests cannot show it reads them - a Rego test hands a rule an input it made
// up, with whatever paths it likes - so, as for the code rules (#779), the
// ledger holds the places, one proof for each:
//
//   - every selector a rule reads through, by a proof of that rule whose
//     broken file the selector picks. Which files a selector picks is asked
//     of OPA, on Conftest's own reading of the broken file, so nothing here
//     says it a second way.
//   - every exclusion in a selector, by a proof that takes it out and is
//     refused: an exclusion is an exemption.
//   - a file nobody has written yet, by a proof that plants one.
//
// A selector's argument is written out, so the place is known before the
// rule runs; a path worked out at run time is a place nothing can name.

// selectorUse is one place a rule reads through lib: a selector, and what it
// was given if it takes an argument.
type selectorUse struct {
	Name     string
	Arg      string
	Computed bool
}

// libRules is what lib declares: each rule by name, whether it takes
// arguments, the input-reading rules it reaches, and the exclusions in it.
type libRules struct {
	function   map[string]bool
	reads      map[string]bool            // names the input itself
	references map[string]map[string]bool // other lib rules its bodies name
	negations  map[string][]string        // the text of each `not` in its bodies
}

func readLib(tree any) libRules {
	l := libRules{map[string]bool{}, map[string]bool{}, map[string]map[string]bool{}, map[string][]string{}}
	root, _ := tree.(map[string]any)
	rules, _ := root["rules"].([]any)
	for _, r := range rules {
		rule, _ := r.(map[string]any)
		head, _ := rule["head"].(map[string]any)
		name, _ := head["name"].(string)
		if name == "" {
			continue
		}
		if args, ok := head["args"].([]any); ok && len(args) > 0 {
			l.function[name] = true
		}
		// The whole rule, head and body: a function's comprehension is in its
		// head.
		if readsInput(rule) > 0 {
			l.reads[name] = true
		}
		if l.references[name] == nil {
			l.references[name] = map[string]bool{}
		}
		for _, v := range varsIn(rule) {
			l.references[name][v] = true
		}
		body, _ := rule["body"].([]any)
		for _, e := range body {
			expr, _ := e.(map[string]any)
			if negated, _ := expr["negated"].(bool); negated {
				if text := locationText(expr); text != "" {
					l.negations[name] = append(l.negations[name], text)
				}
			}
		}
	}
	for name, refs := range l.references {
		for v := range refs {
			if _, declared := l.references[v]; !declared || v == name {
				delete(refs, v)
			}
		}
	}
	return l
}

// base is the rules that read the input which a lib rule reaches, itself
// among them if it reads it.
func (l libRules) base(name string) []string {
	seen, found := map[string]bool{}, map[string]bool{}
	var visit func(string)
	visit = func(n string) {
		if seen[n] {
			return
		}
		seen[n] = true
		if l.reads[n] {
			found[n] = true
		}
		for m := range l.references[n] {
			visit(m)
		}
	}
	visit(name)
	var out []string
	for n := range found {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// varsIn is every variable a syntax tree names.
func varsIn(node any) []string {
	var out []string
	switch v := node.(type) {
	case map[string]any:
		if v["type"] == "var" {
			if s, ok := v["value"].(string); ok {
				out = append(out, s)
			}
		}
		for _, child := range v {
			out = append(out, varsIn(child)...)
		}
	case []any:
		for _, child := range v {
			out = append(out, varsIn(child)...)
		}
	}
	return out
}

// locationText is the source an expression was parsed from, which OPA keeps
// beside it.
func locationText(expr map[string]any) string {
	loc, _ := expr["location"].(map[string]any)
	text, _ := loc["text"].(string)
	b, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// libNamesIn is every name a policy file gives package lib: lib itself, an
// alias it imports it as, and data, through which it can be reached whole.
func libNamesIn(tree any) map[string]bool {
	names := map[string]bool{}
	root, _ := tree.(map[string]any)
	imports, _ := root["imports"].([]any)
	for _, i := range imports {
		imp, _ := i.(map[string]any)
		path, _ := imp["path"].(map[string]any)
		parts, _ := path["value"].([]any)
		if len(parts) == 2 && termValue(parts[0]) == "data" && termValue(parts[1]) == "lib" {
			alias, _ := imp["alias"].(string)
			if alias == "" {
				alias = "lib"
			}
			names[alias] = true
		}
	}
	return names
}

func termValue(node any) string {
	term, _ := node.(map[string]any)
	s, _ := term["value"].(string)
	return s
}

// selectorUses is every selector a rule's file reads through, as lib names
// them: a reference to one, or a call to one that takes an argument.
func selectorUses(tree any, l libRules) []selectorUse {
	names := libNamesIn(tree)
	constants := constantsIn(tree)
	selector := func(ref []any) string {
		switch {
		case len(ref) >= 2 && names[termValue(ref[0])]:
			return termValue(ref[1])
		case len(ref) >= 3 && termValue(ref[0]) == "data" && termValue(ref[1]) == "lib":
			return termValue(ref[2])
		}
		return ""
	}
	var uses []selectorUse
	var walk func(any)
	walk = func(node any) {
		switch v := node.(type) {
		case map[string]any:
			if v["type"] == "call" {
				call, _ := v["value"].([]any)
				if len(call) > 0 {
					op, _ := call[0].(map[string]any)
					ref, _ := op["value"].([]any)
					if name := selector(ref); name != "" && l.function[name] {
						use := selectorUse{Name: name}
						if l.reads[name] {
							// A selector that picks files by what it is given.
							use.Computed = true
							if len(call) > 1 {
								arg, _ := call[1].(map[string]any)
								switch {
								case arg["type"] == "string":
									use.Arg, use.Computed = termValue(arg), false
								case arg["type"] == "var" && constants[termValue(arg)] != "":
									use.Arg, use.Computed = constants[termValue(arg)], false
								}
							}
						}
						uses = append(uses, use)
						for _, a := range call[1:] {
							walk(a)
						}
						return
					}
				}
			}
			if v["type"] == "ref" {
				ref, _ := v["value"].([]any)
				if name := selector(ref); name != "" {
					if _, declared := l.references[name]; declared && !l.function[name] {
						uses = append(uses, selectorUse{Name: name})
					}
				}
			}
			for _, child := range v {
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(tree)
	return uses
}

// constantsIn is every rule of a file that is one written-out string, by
// name: `config := ".github/dependabot.yml"`.
func constantsIn(tree any) map[string]string {
	out := map[string]string{}
	root, _ := tree.(map[string]any)
	rules, _ := root["rules"].([]any)
	for _, r := range rules {
		rule, _ := r.(map[string]any)
		head, _ := rule["head"].(map[string]any)
		value, _ := head["value"].(map[string]any)
		name, _ := head["name"].(string)
		if _, takesArgs := head["args"]; !takesArgs && value["type"] == "string" && name != "" {
			out[name] = termValue(value)
		}
	}
	return out
}

// place is how a selector is asked, in Rego, whether it picks anything.
func place(name, arg string) string {
	if arg == "" {
		return "data.lib." + name
	}
	return fmt.Sprintf("data.lib.%s(%q)", name, arg)
}

// madeFrom is the place a proof breaks to reach a place a rule reads. A plan
// is made from the OpenTofu, by the rule harness, so a rule over the plan or
// its configuration is proved by breaking a file lib.opentofu picks, which
// the harness plans again; every other place is broken where it is.
func madeFrom(p string) string {
	if p == place("plan", "") || p == place("plan_configuration", "") {
		return place("opentofu", "")
	}
	return p
}

// placesOf is every place a rule reads: each selector it names that picks
// files, as narrow as the selector it names rather than the one beneath it,
// so a selector's own exclusions are its places' too. A helper
// that takes an argument and reaches a selector, as steps_of reaches
// actions, reads where the selectors it reaches do.
func placesOf(uses []selectorUse, l libRules) map[string]bool {
	out := map[string]bool{}
	for _, u := range uses {
		switch {
		case l.function[u.Name] && l.reads[u.Name]:
			if !u.Computed {
				out[place(u.Name, u.Arg)] = true
			}
		case l.function[u.Name]:
			for _, b := range l.base(u.Name) {
				if !l.function[b] {
					out[place(b, "")] = true
				}
			}
		case len(l.base(u.Name)) > 0:
			out[place(u.Name, "")] = true
		}
	}
	return out
}

// picks is which of the places pick a proof's broken file: the file as the
// proof leaves it, read by Conftest the way it reads the repository, and the
// places asked of OPA with lib as their only policy.
func picks(t *testing.T, libRel string, m mutation, places map[string]bool) map[string]bool {
	t.Helper()
	tree := repository(t)
	var scratch *parsed.Tree
	if m.Creates() {
		scratch = tree.CopyOf(t)
	} else {
		scratch = tree.CopyOf(t, m.File)
	}
	scratch.Apply(t, m)
	input, _, err := scratch.Invoke(parsed.Run{}, conftestProgram, "parse", "--combine", m.File)
	if err != nil {
		t.Fatalf("conftest could not read %s as %s leaves it: %v", m.File, m.Source, err)
	}
	var asked []string
	for p := range places {
		asked = append(asked, fmt.Sprintf("%q: count(%s) > 0", p, p))
	}
	sort.Strings(asked)
	out, _, err := tree.Invoke(parsed.Run{Stdin: string(input)}, opaProgram, "eval", "--format", "json", "--stdin-input", "-d", parsed.Here(libRel), "{"+strings.Join(asked, ", ")+"}")
	if err != nil {
		t.Fatalf("opa could not ask lib's selectors about %s: %v", m.File, err)
	}
	var answer struct {
		Result []struct {
			Expressions []struct {
				Value map[string]bool `json:"value"`
			} `json:"expressions"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &answer); err != nil || len(answer.Result) != 1 || len(answer.Result[0].Expressions) != 1 {
		t.Fatalf("opa's answer about %s could not be read: %v\n%s", m.File, err, out)
	}
	return answer.Result[0].Expressions[0].Value
}

func TestEverySelectorARuleReadsThroughIsProved(t *testing.T) {
	heavy(t, "reads each proof's broken file with conftest and asks opa which selectors pick it")
	files := policyFiles(t)
	var libRel string
	var lib libRules
	places := map[string]map[string]bool{}
	for rel, tree := range files {
		switch {
		case tree.Package == "lib":
			libRel, lib = rel, readLib(tree.Tree)
		}
	}
	if libRel == "" {
		t.Fatalf("no policy file is package lib")
	}
	for rel, tree := range files {
		rule, ok := strings.CutPrefix(tree.Package, "rules.")
		if !ok || isTestPackage(tree.Package) {
			continue
		}
		uses := selectorUses(tree.Tree, lib)
		for _, u := range uses {
			if u.Computed {
				t.Errorf("%s gives lib.%s a path it works out as it runs. Write the path out, or pick the files by what they are (lib.named), so the place it reads can be named and proved.", rel, u.Name)
			}
		}
		places[rule] = placesOf(uses, lib)
		if len(places[rule]) == 0 {
			t.Errorf("%s reads no file through any selector, so it judges nothing", rel)
		}
	}

	var proofs []mutation
	for _, m := range readLedger(t).Mutations {
		if m.Guard == configurationRulesHarness {
			proofs = append(proofs, m)
		}
	}
	covered := map[string]map[string]bool{}
	planted := false
	for _, m := range proofs {
		planted = planted || m.Creates()
		if strings.HasSuffix(m.File, ".rego") || len(places[m.Rule]) == 0 {
			continue // it breaks a rule, not a file a rule reads
		}
		if covered[m.Rule] == nil {
			covered[m.Rule] = map[string]bool{}
		}
		ask := map[string]bool{}
		for p := range places[m.Rule] {
			ask[madeFrom(p)] = true
		}
		picked := picks(t, libRel, m, ask)
		for p := range places[m.Rule] {
			if picked[madeFrom(p)] {
				covered[m.Rule][p] = true
			}
		}
	}
	if !planted {
		t.Errorf("no proof of %s plants a file, so nothing shows a configuration rule reads a file that is not there today", configurationRulesHarness)
	}
	var missing []string
	for rule, ps := range places {
		for p := range ps {
			if !covered[rule][p] {
				missing = append(missing, fmt.Sprintf("%s reads through %s, and no proof of it breaks a file that picks", rule, p))
			}
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Error(m)
	}
	for name, texts := range lib.negations {
		if len(lib.base(name)) == 0 {
			continue // a helper, which picks no file
		}
		for _, text := range texts {
			proved := false
			for _, m := range proofs {
				if m.File == libRel && strings.Contains(m.Find, text) && !strings.Contains(m.Replace, text) {
					proved = true
				}
			}
			if !proved {
				t.Errorf("lib's selector %s excludes with %q, and no proof takes the exclusion out and is refused, so nothing shows it exempts anything", name, text)
			}
		}
	}
}

func TestSelectorUsesFindsLibByEveryNameAFileGivesIt(t *testing.T) {
	dir := t.TempDir()
	parse := func(src string) any {
		t.Helper()
		file := filepath.Join(dir, "x.rego")
		if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(opaProgram, "parse", "--format", "json", "--json-include", "locations", file).Output()
		if err != nil {
			t.Fatalf("opa parse: %v", err)
		}
		var tree any
		if err := json.Unmarshal(out, &tree); err != nil {
			t.Fatal(err)
		}
		return tree
	}
	lib := readLib(parse(`package lib
import rego.v1
objects contains f if { some f in input; is_object(f.contents) }
named(n) := [f | some f in input; endswith(f.path, n)]
kinds contains f.contents.kind if { some f in objects }
triggers(doc) := {doc}
`))
	if !lib.function["named"] || !lib.reads["named"] || lib.reads["kinds"] || lib.function["kinds"] {
		t.Fatalf("lib read wrong: %+v", lib)
	}
	if got := lib.base("kinds"); len(got) != 1 || got[0] != "objects" {
		t.Errorf("kinds reaches %v, want objects", got)
	}
	uses := selectorUses(parse(`package rules.x
import data.lib as l
import rego.v1
name := "kustomization.yaml"
deny contains "a" if { some f in l.objects; some g in l.named(name); some h in data.lib.kinds }
deny contains "b" if { some f in l.named(sprintf("%s", ["y"])); l.triggers(f) }
`), lib)
	places := placesOf(uses, lib)
	for _, want := range []string{"data.lib.objects", `data.lib.named("kustomization.yaml")`, "data.lib.kinds"} {
		if !places[want] {
			t.Errorf("places %v lack %s", places, want)
		}
	}
	computed := 0
	for _, u := range uses {
		if u.Computed {
			computed++
		}
	}
	if computed != 1 {
		t.Errorf("%d uses were computed, want the one sprintf: %+v", computed, uses)
	}
}
