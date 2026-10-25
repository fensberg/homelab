package repo

import (
	"homelab/tests/parsed"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The gate that enforces coverage, tested by running it.
//
// It was on the block list, and it was the entry that mattered most: this
// script decides whether every other coverage number is acceptable, and
// nothing had ever executed it. The only test that mentioned it read one line
// of the workflow that calls it - which is a change detector, and passes
// forever while the behaviour rots.
//
// Run against a fixture baseline rather than the repository's own, so the
// cases are chosen rather than whatever the project's floors happen to be
// today, and so raising a real floor cannot turn this red.
func runGate(t *testing.T, baseline, language, current string) (stdout, stderr string, code int) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "baseline.json")
	if err := os.WriteFile(path, []byte(baseline), 0o600); err != nil {
		t.Fatalf("writing the fixture baseline: %v", err)
	}

	// Every input this depends on is stated, so the result is about the script
	// rather than about the machine: no inherited GITHUB_STEP_SUMMARY to write
	// into, and the tolerance pinned rather than taken from the environment.
	out, errb, err := repository(t).Invoke(parsed.Run{Env: []string{
		"BASELINE_FILE=" + path,
		"COVERAGE_TOLERANCE=0.5",
		"GITHUB_STEP_SUMMARY=" + filepath.Join(dir, "summary.md"),
	}}, "bash", parsed.Here("scripts/coverage-gate.sh"), language, current)
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return string(out), string(errb), exit.ExitCode()
		}
		t.Fatalf("running the gate: %v", err)
	}
	return string(out), string(errb), 0
}

const gateFixture = `{"baselines": {"go/example": 40, "js": 0}}`

func TestTheCoverageGateHoldsItsFloor(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		current  string
		wantCode int
		why      string
	}{
		{
			name: "above the baseline passes", current: "55.0", wantCode: 0,
			why: "an improvement must not fail, or nobody adds tests",
		},
		{
			name: "exactly at the baseline passes", current: "40.0", wantCode: 0,
			why: "the ratchet holds ground; standing still is explicitly allowed",
		},
		{
			// The tolerance exists because the same code measures a shade
			// differently run to run. It is a tolerance, not a second floor.
			name: "inside the tolerance passes", current: "39.6", wantCode: 0,
			why: "run-to-run wobble must not fail a change that added nothing",
		},
		{
			name: "below the floor fails", current: "39.4", wantCode: 1,
			why: "this is the entire point of the script",
		},
		{
			name: "far below the floor fails", current: "1.0", wantCode: 1,
			why: "a collapse must not be reported as a pass",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, code := runGate(t, gateFixture, "go/example", tc.current)
			if code != tc.wantCode {
				t.Errorf("coverage %s against a baseline of 40 exited %d, want %d - %s\n%s",
					tc.current, code, tc.wantCode, tc.why, stderr)
			}
		})
	}
}

// The failures that are not about the number, and each must be loud rather
// than treated as a pass. A gate that cannot tell whether it should pass has
// to refuse: "I was not told what belongs here" and "nothing is wrong" look
// identical in an exit code, and only one of them is ever true.
func TestTheCoverageGateRefusesWhatItCannotJudge(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, baseline, language, current, mentions string
	}{
		{
			name: "a language with no baseline", baseline: gateFixture,
			language: "go/nothing-declares-this", current: "50",
			mentions: "declares no baseline",
		},
		{
			name: "a percentage that is not a number", baseline: gateFixture,
			language: "go/example", current: "not-a-number",
			mentions: "is not a percentage",
		},
		{
			name: "no baseline file at all", baseline: "", language: "go/example", current: "50",
			mentions: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var code int
			var stderr string
			if tc.baseline == "" {
				// Point it at a path that does not exist, which is the case a
				// typo in the workflow would produce.
				_, errb, err := repository(t).Invoke(parsed.Run{Env: []string{"BASELINE_FILE=" + filepath.Join(t.TempDir(), "absent.json")}},
					"bash", parsed.Here("scripts/coverage-gate.sh"), tc.language, tc.current)
				exit, ok := err.(*exec.ExitError)
				if !ok {
					t.Fatalf("a missing baseline file did not fail the gate at all: %v", err)
				}
				code, stderr = exit.ExitCode(), string(errb)
			} else {
				_, stderr, code = runGate(t, tc.baseline, tc.language, tc.current)
			}

			if code != 2 {
				t.Errorf("exited %d, want 2. A gate that cannot judge must say so distinctly from one that judged and failed - %s", code, stderr)
			}
			if tc.mentions != "" && !strings.Contains(stderr, tc.mentions) {
				t.Errorf("the message does not say %q, so nobody can tell what was wrong:\n%s", tc.mentions, stderr)
			}
		})
	}
}
