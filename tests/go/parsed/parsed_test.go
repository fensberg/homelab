package parsed

import (
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
		if got := tree.FixtureText(t, "site.yaml"); got != c.want {
			t.Errorf("dropping %s left:\n%s", c.drop, got)
		}
	}
	tree := Empty(t)
	tree.Write(t, "bad.yaml", "a: [\n---\n"+block("first"))
	tree.RemoveDocuments(t, "bad.yaml", func(any) bool { return true })
	if got := tree.FixtureText(t, "bad.yaml"); got != "a: [" {
		t.Errorf("a document that does not decode was dropped, or everything else kept:\n%s", got)
	}
}

// Text is given for a fixture a test made, and for nothing else.
func TestOnlyAFixtureGivesItsText(t *testing.T) {
	fixture := Empty(t)
	fixture.Write(t, "a.txt", "written here")
	if fixture.FixtureText(t, "a.txt") != "written here" {
		t.Error("a fixture did not give back what was written")
	}
	if !(&Tree{scratch: true}).scratch || (&Tree{}).fixture {
		t.Error("a tree that is not a fixture says it is one")
	}
}
