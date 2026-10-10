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
	body := "#!/bin/sh\necho \"$*\" > " + asked + "\n" + script + "\n"
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
	drawn, err := Draw("/the/repository", "/a/tree", []string{"an-application/settings"}, "a", "b/two.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if string(drawn["a/one.yaml"]) != `[{"kind":"One"}]` || string(drawn[Built+"an-application/settings"]) != `[]` {
		t.Errorf("the answer came back as %v", drawn)
	}
	said, _ := os.ReadFile(asked)
	want := "run -C /the/repository/scripts/drafter . draw -root /a/tree -build an-application/settings a b/two.yaml"
	if strings.TrimSpace(string(said)) != want {
		t.Errorf("the drafter was asked:\n  %s\nand should have been asked:\n  %s", said, want)
	}
}

// A drafter that refuses is an error carrying its own reason, and one that
// answers with something else is not taken as a drawing of nothing.
func TestADrafterThatRefusesOrAnswersWithSomethingElseIsAnError(t *testing.T) {
	standIn(t, `echo "error: thing.yaml: not YAML that can be read" >&2; exit 1`)
	if _, err := Draw("/r", "/t", nil, "thing.yaml"); err == nil || !strings.Contains(err.Error(), "thing.yaml") {
		t.Errorf("a refusal was not passed on with its reason: %v", err)
	}
	standIn(t, `echo "not a drawing"`)
	if _, err := Draw("/r", "/t", nil, "thing.yaml"); err == nil {
		t.Error("an answer that is not a drawing was taken as one")
	}
}
