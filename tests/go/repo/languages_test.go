package repo

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A language nobody declared is a language nothing checks.
//
// THE GAP THIS CLOSES. Every check in this estate is bound to a file type -
// shellcheck to .sh, tofu to .tf, vitest to .ts, hadolint to Dockerfiles. That
// is deliberate: the one aggregate that held a default opinion about unfamiliar
// files was removed in #365 because it hung more often than it caught anything,
// and each replacement is a dedicated tool with a dedicated subject.
//
// The cost is that adding a .rs or a .py to this repository produces silence.
// Nothing vets it, builds it, lints it, covers it or proves it, and nothing says
// so - because no check has ever heard of that extension. "I have no opinion
// about this file" and "this file is fine" are the same green.
//
// WHY THIS IS NOT SOLVED BY WIDENING THE OTHER GUARDS. It cannot be: a guard
// written for Go's AST cannot read Rust, and demanding that a new language
// arrive with a full toolchain is a bar nobody would clear, so it would be
// worked around. This asks for something much cheaper and strictly honest -
// that somebody WRITE DOWN that the new thing has no tooling. The debt then
// exists, is countable, and can only shrink.
//
// WHAT THIS DELIBERATELY CANNOT CATCH, so nobody reads it as more than it is:
//   - A language sharing an extension with a declared one. Ruby committed as
//     .sh is governed as far as this is concerned.
//   - A file whose kind is declared but whose declared reader has stopped
//     running. This asserts the declaration exists, not that the tool is wired
//     up; the guards that own each tool assert that.
// The shebang half below closes the one case that IS reachable: an extensionless
// script naming an interpreter nothing here runs.

type languageDeclaration struct {
	Governed []struct {
		Kind     string `yaml:"kind"`
		Reader   string `yaml:"read_by"`
		Optional bool   `yaml:"optional"`
	} `yaml:"governed"`
	Ungoverned []struct {
		Kind string `yaml:"kind"`
		What string `yaml:"what"`
	} `yaml:"ungoverned"`
	Interpreters []struct {
		Name   string `yaml:"name"`
		Reader string `yaml:"read_by"`
	} `yaml:"interpreters"`
	UngovernedCeiling int `yaml:"ungoverned_ceiling"`
}

// kindOf classifies a tracked path the way tests/languages.yml describes.
func kindOf(rel string) string {
	base := filepath.Base(rel)
	if !strings.Contains(base, ".") {
		return base // Dockerfile, CODEOWNERS, a githook
	}
	if strings.HasPrefix(base, ".") && strings.Count(base, ".") == 1 {
		return base // .gitignore, .codespellrc
	}
	return base[strings.LastIndex(base, "."):]
}

func loadLanguages(t *testing.T) languageDeclaration {
	t.Helper()
	var decl languageDeclaration
	if err := yaml.Unmarshal([]byte(readRepoFile(t, "tests/languages.yml")), &decl); err != nil {
		t.Fatalf("parsing tests/languages.yml: %v", err)
	}
	return decl
}

func TestEveryKindOfFileIsGovernedOrDeclaredUngoverned(t *testing.T) {
	decl := loadLanguages(t)

	governed := map[string]bool{}
	// Kinds whose absence is the normal state - .github/patches is empty
	// between hand-overs - so a stale-entry failure there would fire on every
	// successful hand-over rather than on a real hole.
	sometimesAbsent := map[string]bool{}
	for _, g := range decl.Governed {
		if g.Kind == "" {
			t.Error("an entry under governed: names no kind")
			continue
		}
		if strings.TrimSpace(g.Reader) == "" {
			t.Errorf(`the governed entry for %q does not say what reads it.

An entry with no reader is an ungoverned kind filed in the wrong list, which is
worse than the hole itself: it reads as a considered decision.`, g.Kind)
		}
		governed[g.Kind] = true
		if g.Optional {
			sometimesAbsent[g.Kind] = true
		}
	}
	ungoverned := map[string]bool{}
	for _, u := range decl.Ungoverned {
		if u.Kind == "" {
			t.Error("an entry under ungoverned: names no kind")
			continue
		}
		if governed[u.Kind] {
			t.Errorf(`%q is listed as both governed and ungoverned.

The two lists mean opposite things and the count below only totals one of them,
so a kind in both is a hole that does not appear in the ceiling. Decide which it
is: if anything reads it, it is governed.`, u.Kind)
		}
		ungoverned[u.Kind] = true
	}

	// The universe, discovered. Not a directory, not a glob: every file this
	// repository tracks, so a new language cannot escape by being put somewhere
	// this check was not told to look.
	present := map[string][]string{}
	for _, rel := range trackedFiles(t) {
		k := kindOf(rel)
		present[k] = append(present[k], rel)
	}

	var kinds []string
	for k := range present {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)

	for _, k := range kinds {
		if governed[k] || ungoverned[k] {
			continue
		}
		example := present[k][0]
		t.Errorf(`nothing in tests/languages.yml covers %q (%d file(s), e.g. %s).

No check in this estate has an opinion about it. Every tool here is bound to a
file type, so this is not linted, not built, not executed, not covered and not
proved - and none of that shows up as a failure anywhere, because no check knows
the kind exists.

If something does read it, add it under governed: and name the tool. If nothing
does, add it under ungoverned: and raise ungoverned_ceiling in the same diff, so
the hole is counted rather than invisible.`, k, len(present[k]), example)
	}

	// A declaration for something that is gone makes the estate look better
	// covered than it is, and leaves an exemption waiting for the next file to
	// land on that kind.
	for _, k := range append(keysOf(governed), keysOf(ungoverned)...) {
		if len(present[k]) == 0 && !sometimesAbsent[k] {
			t.Errorf(`tests/languages.yml declares %q, which this repository no
longer tracks.

Remove it. A stale entry is an exemption nobody chose, inherited by whatever
arrives at that kind next.`, k)
		}
	}

	if n := len(decl.Ungoverned); n > decl.UngovernedCeiling {
		t.Errorf(`%d ungoverned kind(s) are declared and the ceiling is %d.

The ceiling may only fall. Raising it is allowed only in the same change that
adds the entry, and is the moment to ask whether the new thing needs a tool
rather than an exemption.`, n, decl.UngovernedCeiling)
	}
	if decl.UngovernedCeiling > len(decl.Ungoverned) {
		t.Errorf(`the ungoverned ceiling is %d but only %d entries are declared.

Removing an entry without lowering the ceiling leaves room nobody has to
account for - the next hole lands for free.`, decl.UngovernedCeiling, len(decl.Ungoverned))
	}

	const fewestPlausibleKinds = 10
	if len(kinds) < fewestPlausibleKinds {
		t.Fatalf(`only %d kind(s) of file were found, and this repository has more.

The classification has broken, so this proves nothing: every kind it no longer
sees is one it silently stopped asking about.`, len(kinds))
	}
}

func keysOf(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// An executable script names an interpreter, and that is a language too.
//
// This is the one case the extension check above genuinely cannot reach. A file
// with no extension is classified by its own name, so `githooks/pre-push` is
// its own kind and declaring it says nothing about what it is written in. Drop
// in `githooks/deploy` written in Python and the only honest signal is the
// shebang.
//
// So every interpreter named by a shebang must be one this estate declares it
// can check. The point is the same as above: not that a new interpreter is
// forbidden, but that adopting one is a thing somebody does on purpose.
func TestEveryShebangNamesADeclaredInterpreter(t *testing.T) {
	root := repoRoot(t)

	// Declared in tests/languages.yml beside the file kinds, rather than here.
	// A declaration kept in the guard is one a reader has to go and find, and
	// the whole question of what this estate can check belongs in one file -
	// which is also the file .github/sensitive-paths watches.
	declared := map[string]bool{}
	for _, i := range loadLanguages(t).Interpreters {
		if strings.TrimSpace(i.Reader) == "" {
			t.Errorf("the interpreter %q is declared with nothing that reads it", i.Name)
		}
		declared[i.Name] = true
	}
	if len(declared) == 0 {
		t.Fatal("tests/languages.yml declares no interpreters, so this would accept " +
			"any script in any language")
	}

	checked := 0
	var undeclared []string
	for _, rel := range trackedMatching(t, authoredHere) {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		interp, ok := parseShebang(string(body))
		if !ok {
			continue
		}
		checked++
		if !declared[interp] {
			undeclared = append(undeclared, rel+" -> "+interp)
		}
	}

	for _, u := range undeclared {
		t.Errorf(`%s names an interpreter this estate has no tooling for.

A script is a program whichever language it is written in, and this one is
executable. Nothing here lints it, type-checks it or requires it to be covered,
and because the file has no extension saying so, the kind check cannot see it
either.

Add the interpreter to this guard's declared set together with whatever will
read its files, or write the script in something already governed.`, u)
	}

	const fewestShebangs = 4
	if checked < fewestShebangs {
		t.Fatalf(`only %d shebang(s) were found, and githooks/ alone holds four.

The parse has stopped matching, so a script in any language at all would now
pass this unread.`, checked)
	}
}

// parseShebang returns the interpreter a file names, resolving `env`.
func parseShebang(body string) (string, bool) {
	if !strings.HasPrefix(body, "#!") {
		return "", false
	}
	line := body
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	fields := strings.Fields(strings.TrimPrefix(line, "#!"))
	if len(fields) == 0 {
		return "", false
	}
	interp := filepath.Base(fields[0])
	// `#!/usr/bin/env bash` - the interpreter is the argument, and any flags
	// before it (env -S) are not it either.
	if interp == "env" {
		for _, f := range fields[1:] {
			if !strings.HasPrefix(f, "-") && !strings.Contains(f, "=") {
				return f, true
			}
		}
		return "env", true
	}
	return interp, true
}
