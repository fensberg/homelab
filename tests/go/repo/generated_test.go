package repo

import (
	"go/token"
	"path"
	"sort"
	"strings"
	"testing"

	"homelab/tests/parsed"
)

// A file named *.generated.* is written by a program from what it is made
// from, and never by hand: a value declared once is copied, by its
// generator, into a language that cannot read the declaration where it runs.
// The copy is only the declaration while the generator would write it as it
// is (agreed 2026-10-29, with go generate as the generator).
//
// REMADE, NOT READ. In a copy of the tree every generated file is removed
// and every generator run; nothing may then differ from what is committed.
// So a file edited by hand, a declaration changed and not regenerated, a
// generated file no generator makes, and a generator that writes anything
// else are each a change the copy shows, and none needs reading the text.
//
// A generator is found by what it is: a Go file that says go:generate.
func TestEveryGeneratedFileIsWhatItsGeneratorMakes(t *testing.T) {
	heavy(t, "builds and runs every generator in a copy of the tree")
	tree := repository(t)
	var made []string
	generators := map[string]bool{}
	fset := token.NewFileSet()
	for _, rel := range tree.Files(t) {
		if strings.Contains(path.Base(rel), ".generated.") {
			made = append(made, rel)
		}
		if !strings.HasSuffix(rel, ".go") {
			continue
		}
		for _, group := range tree.Go(t, fset, rel).Comments {
			for _, c := range group.List {
				if strings.HasPrefix(c.Text, "//go:generate ") {
					generators[path.Dir(rel)] = true
				}
			}
		}
	}
	if len(made) == 0 {
		t.Fatal("no file named *.generated.* was found, so nothing was held to what makes it")
	}
	if len(generators) == 0 {
		t.Fatal("no Go file says go:generate, so nothing could remake the generated files")
	}
	sort.Strings(made)
	copied := tree.Copy(t)
	for _, rel := range made {
		copied.Remove(t, rel)
	}
	var dirs []string
	for dir := range generators {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		if _, errs, err := copied.Invoke(parsed.Run{Dir: dir}, "go", "generate", "."); err != nil {
			t.Fatalf("the generator in %s does not run: %v\n%s", dir, err, errs)
		}
	}
	// The copy holds the tree as it is committed, staged: against that, what
	// the generators changed or removed, and what they wrote that is new.
	var changed []string
	for _, args := range [][]string{{"diff", "--name-only"}, {"ls-files", "--others", "--exclude-standard"}} {
		out, errs, err := copied.Invoke(parsed.Run{}, "git", args...)
		if err != nil {
			t.Fatalf("git %s in the copy: %v\n%s", strings.Join(args, " "), err, errs)
		}
		if s := strings.TrimSpace(string(out)); s != "" {
			changed = append(changed, s)
		}
	}
	if len(changed) > 0 {
		t.Errorf("regenerating every generated file does not give what is committed:\n\n%s\n\n"+
			"A generated file is never edited by hand. Change what it is made from and run task generate; "+
			"a file named *.generated.* that no generator writes is not one.", strings.Join(changed, "\n"))
	}
}
