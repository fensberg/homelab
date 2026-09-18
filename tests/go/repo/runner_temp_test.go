package repo

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// No workflow step reads $TMPDIR.
//
// GitHub-hosted runners do not set it, and every run step here uses `set -u`,
// so a reference to it is not a temp directory - it is "TMPDIR: unbound
// variable" and a step that fails before doing anything. That is what happened
// to the expedite duty: #400 moved SteamCMD's output into "$TMPDIR/steam.out"
// on 16 September, the redirect failed, SteamCMD never ran, and every daily pass
// after it reported "no public build" - a symptom that pointed at Valve rather
// than at the line that caused it (#440). Actions sets $RUNNER_TEMP for exactly
// this, on every runner, and cleans it up after the job.
//
// Read from the workflows as they will be once outstanding patches apply, so a
// fix arriving as a patch is judged by what it will be.
var tmpdirReference = regexp.MustCompile(`\$\{?TMPDIR\b`)

func TestNoWorkflowStepReadsTMPDIR(t *testing.T) {
	workflows := intendedWorkflows(t)
	if len(workflows) == 0 {
		t.Fatal("no workflows were read, so nothing was checked")
	}
	names := make([]string, 0, len(workflows))
	for name := range workflows {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		for n, line := range strings.Split(workflows[name], "\n") {
			trimmed := strings.TrimSpace(line)
			// A comment may name the variable in order to say not to use it.
			if strings.HasPrefix(trimmed, "#") {
				continue
			}
			if tmpdirReference.MatchString(line) {
				t.Errorf("%s:%d reads $TMPDIR: %s\n\n"+
					"GitHub-hosted runners do not set TMPDIR, so under `set -u` this is "+
					"\"TMPDIR: unbound variable\" and the step fails before doing anything - "+
					"which is how a week of game server updates failed as \"no public build\" "+
					"(#440). Use $RUNNER_TEMP, which Actions sets on every runner.",
					name, n+1, trimmed)
			}
		}
	}
}
