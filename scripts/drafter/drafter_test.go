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
		"broken.yaml": "kind: [never closed",
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

// The keys a file writes twice are named, by the mapping they are twice in.
// The same name in two mappings is not twice: three steps that each take a
// `ref` are three mappings. And a line inside a block of text is not a key.
func TestTheKeysAFileWritesTwiceAreNamed(t *testing.T) {
	root := put(t, map[string]string{
		"twice.yaml": "steps:\n  - with:\n      ref: a\n      ref: b\n  - with:\n      ref: c\nname: one\nname: two\n",
		"clean.yaml": "steps:\n  - with:\n      ref: a\n  - with:\n      ref: b\n  - run: |\n      name: not a key\n      name: still not a key\n",
	})
	var out, complaints bytes.Buffer
	if code := duplicates([]string{"-root", root, "twice.yaml", "clean.yaml"}, &out, &complaints); code != 0 {
		t.Fatalf("naming the keys written twice failed: %s", complaints.String())
	}
	var found map[string][]string
	if err := json.Unmarshal(out.Bytes(), &found); err != nil {
		t.Fatal(err)
	}
	if strings.Join(found["twice.yaml"], ",") != "name,ref" {
		t.Errorf("the keys written twice were named as %v", found["twice.yaml"])
	}
	if names, read := found["clean.yaml"]; !read || len(names) != 0 {
		t.Errorf("a file that writes no key twice was answered with %v", names)
	}
	out.Reset()
	if code := duplicates([]string{"-root", root, "absent.yaml"}, &out, &complaints); code == 0 {
		t.Error("a file that is not there was answered for")
	}
	if code := duplicates([]string{"-root", root}, &out, &complaints); code != 2 {
		t.Error("naming the duplicates of nothing was not refused as a mistake in the asking")
	}
}

// The program itself draws what it is asked on its own arguments, names
// what it does when asked something else, and builds a directory with the
// cluster's own tool.
func TestTheProgramItselfDrawsWhatItIsAsked(t *testing.T) {
	root := put(t, map[string]string{"one.yaml": "kind: One\n", "settings/kustomization.yaml": "resources: []\n"})
	tools := t.TempDir()
	// Stands in for the cluster's tool: says which directory it was asked to
	// build, as a document.
	script := "#!/bin/sh\n[ \"$1\" = kustomize ] || exit 1\necho \"kind: Built\"\necho \"from: $2\"\n"
	if err := os.WriteFile(filepath.Join(tools, "kubectl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))

	var out, complaints bytes.Buffer
	if code := run([]string{"draw", "-root", root, "-build", "settings", "one.yaml"}, &out, &complaints); code != 0 {
		t.Fatalf("the program did not draw what it was asked (%d): %s", code, complaints.String())
	}
	if !strings.Contains(out.String(), `"kind":"One"`) || !strings.Contains(out.String(), `"kind":"Built"`) || !strings.Contains(out.String(), filepath.Join(root, "settings")) {
		t.Errorf("a file and a built directory were drawn as: %s", out.String())
	}
	if code := run([]string{"redraw"}, &out, &complaints); code != 2 || !strings.Contains(complaints.String(), "usage: drafter draw") {
		t.Errorf("asked something it does not do, the program answered %d: %s", code, complaints.String())
	}
	if _, err := kustomized(filepath.Join(root, "settings")); err != nil {
		t.Errorf("building with the cluster's tool: %v", err)
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := kustomized(filepath.Join(root, "settings")); err == nil {
		t.Error("a directory was built with no tool to build it")
	}

	// And as it is started: on its own arguments, leaving quietly when it
	// has drawn what was asked.
	args := os.Args
	t.Cleanup(func() { os.Args = args })
	os.Args = []string{args[0], "draw", "-root", root, "one.yaml"}
	main()
}
