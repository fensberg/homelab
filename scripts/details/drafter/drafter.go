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
	"os/exec"
	"path/filepath"
	"strings"
)

// Built is what a built directory is named by in a drawing, before its
// path.
const Built = "built:"

// Draw has the drafter redraw the paths, and the directories to be built,
// all taken from root. The answer is each file by its path as it was given
// or reached, and each built directory by Built and its path.
//
// The drafter is run from the repository's own source, as the contractor
// runs the safety officer: there is no installed copy to go stale.
func Draw(repository, root string, builds []string, paths ...string) (map[string]json.RawMessage, error) {
	args := []string{"run", "-C", filepath.Join(repository, "scripts", "drafter"), ".", "draw", "-root", root}
	for _, dir := range builds {
		args = append(args, "-build", dir)
	}
	args = append(args, paths...)
	// Every argument is a path this program was given or worked out, passed
	// as itself and never through a shell.
	cmd := exec.Command("go", args...)
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
