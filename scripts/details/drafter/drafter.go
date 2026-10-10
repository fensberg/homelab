// Package drafter is how a program asks the drafter to redraw something.
//
// The drafter (scripts/drafter) is the one program here that takes a library
// to read a format with. Every other program reads what the estate declares
// through it, as JSON, which the standard library reads - so a program that
// holds a key reads its own estate properly without taking a dependency to
// do it, and no program reads a format by matching its lines.
package drafter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"homelab/details/repopath"
)

// Built is what a built directory is named by in a drawing, before its
// path.
const Built = "built:"

// Draw has the drafter redraw the paths, and the directories to be built,
// all taken from root. The answer is each file by its path as it was given
// or reached, and each built directory by Built and its path.
//
// The drafter is run from this checkout's own source, as the contractor
// runs the safety officer: there is no installed copy to go stale.
//
// IT IS RUN WITH NOTHING OF THE CALLER'S. The drafter is the one program
// here with somebody else's code in it, and some of what calls it holds a
// vault token in its environment. So it is given the few variables Go and
// the cluster's own tool need to run, and nothing else: whatever its
// library did, it could not read a credential it was never handed.
func Draw(root string, builds []string, paths ...string) (map[string]json.RawMessage, error) {
	repository, err := repopath.Root()
	if err != nil {
		return nil, err
	}
	args := []string{"run", "-C", filepath.Join(repository, "scripts", "drafter"), ".", "draw", "-root", root}
	for _, dir := range builds {
		args = append(args, "-build", dir)
	}
	args = append(args, paths...)
	// Every argument is a path this program was given or worked out, passed
	// as itself and never through a shell.
	cmd := exec.Command("go", args...)
	cmd.Env = handed(os.Environ())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("the drafter could not redraw what was asked: %s", strings.TrimSpace(stderr.String()))
	}
	drawn := map[string]json.RawMessage{}
	if err := json.Unmarshal(out, &drawn); err != nil {
		return nil, fmt.Errorf("the drafter's answer is not what it gives: %w", err)
	}
	return drawn, nil
}

// handed is what of an environment the drafter is given: where programs
// are, where a home and a temporary directory are, and Go's own settings.
func handed(environment []string) []string {
	var out []string
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if name == "PATH" || name == "HOME" || name == "TMPDIR" || strings.HasPrefix(name, "GO") {
			out = append(out, entry)
		}
	}
	return out
}

// Text has the drafter redraw something a program holds and no file does: a
// file as it was at a commit, or one a test wrote. kind is the ending such a
// file would have, which is what says how it is read.
func Text(kind string, body []byte) (json.RawMessage, error) {
	dir, err := os.MkdirTemp("", "to-draw-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	name := "it" + kind
	if err := os.WriteFile(filepath.Join(dir, name), body, 0o600); err != nil {
		return nil, err
	}
	drawn, err := Draw(dir, nil, name)
	if err != nil {
		return nil, err
	}
	return drawn[name], nil
}

// Documents is a YAML text as the list of its documents, each a mapping.
// The commonest thing asked of the drafter, so it is asked once here.
func Documents(body []byte) ([]map[string]any, error) {
	drawn, err := Text(".yaml", body)
	if err != nil {
		return nil, err
	}
	var docs []map[string]any
	if err := json.Unmarshal(drawn, &docs); err != nil {
		return nil, fmt.Errorf("what was drawn is not a list of mappings: %w", err)
	}
	return docs, nil
}

// Duplicates has the drafter name every key a YAML text writes twice in one
// mapping. Drawing such a text is refused, so this is asked instead by
// whoever needs to know which keys they are.
func Duplicates(body []byte) ([]string, error) {
	repository, err := repopath.Root()
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "to-read-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	const name = "it.yaml"
	if err := os.WriteFile(filepath.Join(dir, name), body, 0o600); err != nil {
		return nil, err
	}
	cmd := exec.Command("go", "run", "-C", filepath.Join(repository, "scripts", "drafter"), ".", "duplicates", "-root", dir, name)
	cmd.Env = handed(os.Environ())
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("the drafter could not read what was asked: %s", strings.TrimSpace(stderr.String()))
	}
	found := map[string][]string{}
	if err := json.Unmarshal(out, &found); err != nil {
		return nil, fmt.Errorf("the drafter's answer is not what it gives: %w", err)
	}
	return found[name], nil
}
