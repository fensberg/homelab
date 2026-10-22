package parsed

import (
	"runtime"
	"strings"
	"testing"
)

// A document taken out leaves every other one as it was written - comments,
// order and all - and one that does not decode stays.
func TestRemoveDocumentsTakesOutWhatItIsToldAndLeavesTheRest(t *testing.T) {
	block := func(name string) string {
		return "# " + name + "'s release\nkind: OCIRepository\nmetadata:\n  name: " + name + "\n"
	}
	named := func(name string) func(any) bool {
		return func(doc any) bool {
			m, _ := doc.(map[string]any)
			meta, _ := m["metadata"].(map[string]any)
			return meta["name"] == name
		}
	}
	for _, c := range []struct {
		drop, want string
	}{
		{"first", "---\n" + block("second")},
		{"second", "---\n" + strings.TrimSuffix(block("first"), "\n")},
		{"absent", "---\n" + block("first") + "---\n" + block("second")},
	} {
		tree := Empty(t)
		tree.Write(t, "site.yaml", "---\n"+block("first")+"---\n"+block("second"))
		tree.RemoveDocuments(t, "site.yaml", named(c.drop))
		if got := tree.OwnText(t, "site.yaml"); got != c.want {
			t.Errorf("dropping %s left:\n%s", c.drop, got)
		}
	}
	tree := Empty(t)
	tree.Write(t, "bad.yaml", "a: [\n---\n"+block("first"))
	tree.RemoveDocuments(t, "bad.yaml", func(any) bool { return true })
	if got := tree.OwnText(t, "bad.yaml"); got != "a: [" {
		t.Errorf("a document that does not decode was dropped, or everything else kept:\n%s", got)
	}
}

// Text is given for what a test, or a program it ran, wrote, and for nothing
// else: never the repository's, nor a copy of one of its files.
func TestOnlyATestsOwnFilesGiveTheirText(t *testing.T) {
	fixture := Empty(t)
	fixture.Write(t, "a.txt", "written here")
	if fixture.OwnText(t, "a.txt") != "written here" {
		t.Error("a fixture did not give back what was written")
	}
	scratch := fixture.CopyOf(t, "a.txt")
	if !refuses(t, func(tb testing.TB) { scratch.OwnText(tb, "a.txt") }) {
		t.Error("a copied file gave its text back")
	}
	scratch.Write(t, "a.txt", "rewritten")
	if scratch.OwnText(t, "a.txt") != "rewritten" {
		t.Error("a file the test wrote over a copy did not give back what it wrote")
	}
	scratch.Write(t, "b.txt", "new")
	if scratch.OwnText(t, "b.txt") != "new" {
		t.Error("a file the test wrote beside a copy did not give back what it wrote")
	}
	if !refuses(t, func(tb testing.TB) { Repository(tb).OwnText(tb, "CLAUDE.md") }) {
		t.Error("the repository gave a file's text back")
	}
}

// refuses reports whether f fails the test it is given.
func refuses(t *testing.T, f func(testing.TB)) bool {
	r := &recorder{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		f(r)
	}()
	<-done
	return r.failed
}

type recorder struct {
	testing.TB
	failed bool
}

func (r *recorder) Fatalf(string, ...any) { r.failed = true; runtime.Goexit() }
func (r *recorder) Fatal(...any)          { r.failed = true; runtime.Goexit() }
func (r *recorder) Errorf(string, ...any) { r.failed = true }
