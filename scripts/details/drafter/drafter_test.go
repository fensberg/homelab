package drafter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// standIn puts a `go` ahead of the real one that says what it was asked and
// answers as the drafter would, or fails as it would.
func standIn(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	asked := filepath.Join(dir, "asked")
	body := "#!/bin/sh\necho \"$*\" > " + asked + "\nenv > " + asked + ".env\n" + script + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return asked
}

// The drafter is asked for exactly what was named, from where it was said
// to be, and its answer comes back by path.
func TestTheDrafterIsAskedForWhatWasNamedAndItsAnswerComesBackByPath(t *testing.T) {
	asked := standIn(t, `echo '{"a/one.yaml":[{"kind":"One"}],"built:an-application/settings":[]}'`)
	t.Setenv("A_VAULT_TOKEN", "not for the drafter")
	drawn, err := Draw("/a/tree", []string{"an-application/settings"}, "a", "b/two.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if string(drawn["a/one.yaml"]) != `[{"kind":"One"}]` || string(drawn[Built+"an-application/settings"]) != `[]` {
		t.Errorf("the answer came back as %v", drawn)
	}
	said, _ := os.ReadFile(asked)
	want := ". draw -root /a/tree -build an-application/settings a b/two.yaml"
	if !strings.HasSuffix(strings.TrimSpace(string(said)), want) || !strings.Contains(string(said), filepath.Join("scripts", "drafter")) {
		t.Errorf("the drafter was asked:\n  %s\nand should have been run from its own source and asked:\n  %s", said, want)
	}
	// And handed nothing of the caller's but what it needs to run.
	environment, _ := os.ReadFile(asked + ".env")
	if strings.Contains(string(environment), "A_VAULT_TOKEN") {
		t.Error("the drafter was handed a variable of the caller's that it has no use for, and the caller may hold a credential in one")
	}
	if !strings.Contains(string(environment), "PATH=") {
		t.Error("the drafter was not handed a PATH, so it could not find the tools it runs")
	}
}

// A drafter that refuses is an error carrying its own reason, and one that
// answers with something else is not taken as a drawing of nothing.
func TestADrafterThatRefusesOrAnswersWithSomethingElseIsAnError(t *testing.T) {
	standIn(t, `echo "error: thing.yaml: not YAML that can be read" >&2; exit 1`)
	if _, err := Draw("/t", nil, "thing.yaml"); err == nil || !strings.Contains(err.Error(), "thing.yaml") {
		t.Errorf("a refusal was not passed on with its reason: %v", err)
	}
	standIn(t, `echo "not a drawing"`)
	if _, err := Draw("/t", nil, "thing.yaml"); err == nil {
		t.Error("an answer that is not a drawing was taken as one")
	}
}

// Text a program holds is redrawn by the real drafter as a file of that
// kind would be. This one is not stood in for: it is the whole path, from
// the text to the documents, that every reader here now rests on.
func TestTextIsRedrawnAsAFileOfItsKindWouldBe(t *testing.T) {
	docs, err := Documents([]byte("# a comment is not a document\nkind: First\nitems:\n  - name: \"quoted\"\n    path:\n      on/the/line/below\n---\nkind: Second\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 || docs[0]["kind"] != "First" || docs[1]["kind"] != "Second" {
		t.Fatalf("two documents were redrawn as %v", docs)
	}
	item := docs[0]["items"].([]any)[0].(map[string]any)
	if item["name"] != "quoted" || item["path"] != "on/the/line/below" {
		t.Errorf("a quoted value and one on the line below were read as %v", item)
	}
	if _, err := Documents([]byte("kind: [unclosed")); err == nil {
		t.Error("text that is not YAML was redrawn as documents")
	}
}
