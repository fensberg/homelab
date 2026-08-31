package repo

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// GitHub runs a `run:` step under `bash -e {0}` unless the shell is named.
// That has -e but not -o pipefail, so in `a | b` only b's status is the step's
// status: a command that fails while feeding one that succeeds is a green
// step. Naming the shell explicitly selects
// `bash --noprofile --norc -eo pipefail {0}`.
//
// This repository has paid for that default twice, and neither time looked
// like a failure:
//
//   - `tofu plan | tee` took its status from tee, so a job that had already
//     failed reported success and posted an empty pull request comment.
//   - `rclone version | head -1` took its status from head, so the runner
//     image's own verification step passed whether or not rclone ran. The
//     check had never checked anything.
//
// Neither is visible in a diff, and no linter reports it, because nothing is
// wrong with the text - the default is wrong. So it is asserted here instead,
// on every workflow rather than only those with a `run:` step today, so that
// the one added tomorrow is covered before it is written.
var pipefailDefault = regexp.MustCompile(`(?m)^defaults:\s*\n\s+run:\s*\n\s+shell:\s+bash\s*$`)

func TestEveryWorkflowDefaultsToAPipefailShell(t *testing.T) {
	dir := filepath.Join(repoRoot(t), ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	var checked int
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yml") {
			continue
		}
		checked++
		if !pipefailDefault.MatchString(readFile(t, filepath.Join(dir, e.Name()))) {
			t.Errorf(`.github/workflows/%s does not set a pipefail shell.

Add this above its jobs::

    defaults:
      run:
        shell: bash

Without it every `+"`run:`"+` step in the file runs under `+"`bash -e {0}`"+`, where a
failing command in a pipeline is hidden by whatever it pipes into.`, e.Name())
		}
	}
	if checked == 0 {
		t.Fatal("found no workflows, so this test proves nothing")
	}
}
