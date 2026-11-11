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
func mainFailing(t *testing.T, event, runs, jobs string, before ...string) (string, error) {
	t.Helper()
	bin := t.TempDir()
	fake := `#!/usr/bin/env bash
case "$*" in
*/attempts/*/jobs*) answer="$BEFORE" ;;
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
		"BEFORE=" + strings.Join(before, ""),
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

// A failed run on main being rerun is not finished, so the run before it
// would be read instead, and a pull request would owe nothing for as long as
// the rerun took (#856). It is read as the attempt before it.
func TestARerunOfAFailureOnMainStillCounts(t *testing.T) {
	light(t)
	const rerunning = `{"workflow_runs":[{"id":7,"status":"in_progress","conclusion":null,"run_attempt":2},{"id":6,"status":"completed","conclusion":"success","run_attempt":1}]}`
	out, err := mainFailing(t, "pull_request", rerunning, `{"jobs":[]}`, `{"jobs":[{"name":"test / mutation (2 of 4)","conclusion":"failure"},{"name":"test / app removal","conclusion":"timed_out"}]}`)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, w := range []string{"mutation=true", "removal=true"} {
		if !strings.Contains(out, w) {
			t.Errorf("main's failed run is being rerun and a pull request was told %q, want %q", out, w)
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
