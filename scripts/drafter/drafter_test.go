package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// put writes files under a directory of the test's own and returns it.
func put(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func unbuildable(string) ([]byte, error) { return nil, errors.New("nothing here is built") }

// drawing runs the verb and returns what it drew, by path.
func drawing(t *testing.T, build func(string) ([]byte, error), args ...string) (int, map[string]json.RawMessage, string) {
	t.Helper()
	var out, complaints bytes.Buffer
	code := draw(args, &out, &complaints, build)
	drawn := map[string]json.RawMessage{}
	if code == 0 {
		if err := json.Unmarshal(out.Bytes(), &drawn); err != nil {
			t.Fatalf("what was drawn is not JSON: %v\n%s", err, out.String())
		}
	}
	return code, drawn, complaints.String()
}

// A YAML file is redrawn as the list of its documents, each as it was
// written, and an empty document is not one.
func TestAYAMLFileIsRedrawnAsTheListOfItsDocuments(t *testing.T) {
	root := put(t, map[string]string{"a/two.yaml": "---\nkind: First\nspec:\n  count: 3\n  names: [x, y]\n---\n---\nkind: Second\n"})
	code, drawn, said := drawing(t, unbuildable, "-root", root, "a/two.yaml")
	if code != 0 {
		t.Fatalf("drawing a YAML file failed: %s", said)
	}
	var docs []struct {
		Kind string
		Spec struct {
			Count int
			Names []string
		}
	}
	if err := json.Unmarshal(drawn["a/two.yaml"], &docs); err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 || docs[0].Kind != "First" || docs[0].Spec.Count != 3 || len(docs[0].Spec.Names) != 2 || docs[1].Kind != "Second" {
		t.Errorf("two documents and an empty one were redrawn as %+v", docs)
	}
}

// YAML lets a key be a number, and JSON does not. Such a key is written as
// it reads, and the file is not refused for it. And a workflow's `on:` stays
// the word it is: an older reading of YAML takes it for a truth.
func TestAKeyThatIsNotTextIsWrittenAsItReads(t *testing.T) {
	root := put(t, map[string]string{"w.yml": "on:\n  push: {}\n404: missing\ntrue: also a key\n"})
	code, drawn, said := drawing(t, unbuildable, "-root", root, "w.yml")
	if code != 0 {
		t.Fatalf("a file with a key that is not text was refused: %s", said)
	}
	text := string(drawn["w.yml"])
	for _, key := range []string{`"on"`, `"404"`, `"true"`} {
		if !strings.Contains(text, key) {
			t.Errorf("the key %s was lost in the redrawing: %s", key, text)
		}
	}
}

// A directory is read all the way down, and what the drafter was not taught
// to read is passed over there. Each file is named as it was reached.
func TestADirectoryIsReadAllTheWayDown(t *testing.T) {
	root := put(t, map[string]string{
		"tree/one.yaml":        "kind: One\n",
		"tree/deep/two.yml":    "kind: Two\n",
		"tree/deep/three.json": `{"kind":"Three"}`,
		"tree/versions.env":    "# pinned\nTOOL_VERSION=1.2.3\nQUOTED=\"v4\"\n\n",
		"tree/README.md":       "prose, which is passed over",
		"elsewhere/four.yaml":  "kind: Four\n",
	})
	code, drawn, said := drawing(t, unbuildable, "-root", root, "tree")
	if code != 0 {
		t.Fatalf("drawing a directory failed: %s", said)
	}
	if len(drawn) != 4 {
		t.Errorf("four files in the directory can be read, and %d were drawn: %v", len(drawn), drawn)
	}
	if string(drawn["tree/deep/three.json"]) != `{"kind":"Three"}` {
		t.Errorf("a JSON file was not passed through as itself: %s", drawn["tree/deep/three.json"])
	}
	var env map[string]string
	if err := json.Unmarshal(drawn["tree/versions.env"], &env); err != nil || env["TOOL_VERSION"] != "1.2.3" || env["QUOTED"] != "v4" || len(env) != 2 {
		t.Errorf("a file of names and values was redrawn as %v (%v)", env, err)
	}
}

// What cannot be read is refused and named, never drawn as nothing: a file
// of the wrong kind named outright, YAML that is broken, a line that is not
// a name and a value, a path that is not there.
func TestWhatCannotBeReadIsRefusedAndNamed(t *testing.T) {
	root := put(t, map[string]string{
		"notes.txt":   "prose",
		"broken.yaml": "kind: [unclosed",
		"broken.json": "{",
		"broken.env":  "A=1\nnot a setting\n",
	})
	for path, names := range map[string]string{
		"notes.txt":   "not been taught",
		"broken.yaml": "broken.yaml",
		"broken.json": "broken.json",
		"broken.env":  "line 2",
		"absent.yaml": "absent.yaml",
	} {
		code, _, said := drawing(t, unbuildable, "-root", root, path)
		if code == 0 || !strings.Contains(said, names) {
			t.Errorf("%s was drawn, or refused without saying %q: %s", path, names, said)
		}
	}
	if code, _, _ := drawing(t, unbuildable, "-root", root); code != 2 {
		t.Errorf("drawing nothing was not refused as a mistake in the asking (%d)", code)
	}
}

// A directory that is assembled is built first and redrawn as what comes
// out, under a name that says it was built.
func TestABuiltDirectoryIsRedrawnAsWhatComesOut(t *testing.T) {
	root := put(t, map[string]string{"app/settings/kustomization.yaml": "resources: []\n"})
	asked := ""
	build := func(dir string) ([]byte, error) {
		asked = dir
		return []byte("kind: Assembled\n---\nkind: AlsoAssembled\n"), nil
	}
	code, drawn, said := drawing(t, build, "-root", root, "-build", "app/settings")
	if code != 0 {
		t.Fatalf("drawing a built directory failed: %s", said)
	}
	if asked != filepath.Join(root, "app", "settings") {
		t.Errorf("the directory built was %q", asked)
	}
	var docs []map[string]string
	if err := json.Unmarshal(drawn["built:app/settings"], &docs); err != nil || len(docs) != 2 || docs[0]["kind"] != "Assembled" {
		t.Errorf("what was built was redrawn as %v (%v)", docs, err)
	}
	if code, _, said := drawing(t, unbuildable, "-root", root, "-build", "app/settings"); code == 0 || !strings.Contains(said, "app/settings") {
		t.Errorf("a directory that could not be built was drawn, or not named: %s", said)
	}
}
