package repo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homelab/tests/parsed"
)

// mainFailing runs the script with curl replaced on PATH by one that answers
// the two questions it asks. An empty answer fails, as curl does on an error.
func mainFailing(t *testing.T, event, runs, jobs string) (string, error) {
	t.Helper()
	bin := t.TempDir()
	fake := `#!/usr/bin/env bash
case "$*" in
*/runs/*/jobs*) answer="$JOBS" ;;
*) answer="$RUNS" ;;
esac
[ -n "$answer" ] || exit 22
printf '%s' "$answer"
`
	if err := os.WriteFile(filepath.Join(bin, "curl"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	out, errs, err := repository(t).Invoke(parsed.Run{Env: []string{
		"PATH=" + bin + ":" + os.Getenv("PATH"),
		"EVENT=" + event, "REPO=o/r", "GITHUB_TOKEN=t", "RUNS=" + runs, "JOBS=" + jobs,
	}, Bare: true}, "bash", "scripts/main-failing.sh")
	return strings.TrimSpace(string(out)) + string(errs), err
}

func TestAPullRequestRunsWhatMainIsFailing(t *testing.T) {
	light(t)
	const finished = `{"workflow_runs":[{"id":7,"status":"in_progress","conclusion":null},{"id":6,"status":"completed","conclusion":"cancelled"},{"id":5,"status":"completed","conclusion":"failure"}]}`
	for _, c := range []struct {
		name, event, runs, jobs string
		want                    []string
	}{
		{"main is green", "pull_request", finished, `{"jobs":[{"name":"test / app removal","conclusion":"success"}]}`,
			[]string{"mutation=false", "removal=false"}},
		{"main has never run", "pull_request", `{"workflow_runs": []}`, "",
			[]string{"mutation=false", "removal=false"}},
		{"the removal check is failing", "pull_request", finished, `{"jobs":[{"name":"test / app removal","conclusion":"failure"}]}`,
			[]string{"mutation=false", "removal=true"}},
		{"a part of the ledger is failing", "merge_group", finished, `{"jobs":[{"name":"test / mutation (3 of 4)","conclusion":"timed_out"}]}`,
			[]string{"mutation=true", "removal=false"}},
		{"on main", "push", "", "",
			[]string{"mutation=true", "removal=true"}},
	} {
		out, err := mainFailing(t, c.event, c.runs, c.jobs)
		if err != nil {
			t.Errorf("%s: %v\n%s", c.name, err, out)
			continue
		}
		for _, w := range c.want {
			if !strings.Contains(out, w) {
				t.Errorf("%s: said %q, want %q", c.name, out, w)
			}
		}
	}
}

// Not knowing is not green: a merge is not let through because GitHub did not
// answer.
func TestNotKnowingWhatMainIsFailingFails(t *testing.T) {
	light(t)
	if out, err := mainFailing(t, "pull_request", "", ""); err == nil {
		t.Errorf("no answer from GitHub passed: %s", out)
	}
}
