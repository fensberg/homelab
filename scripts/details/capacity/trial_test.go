package capacity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A trial is started from the programs already delivered, named to the
// starter, and stopped and removed whatever happens; a starter that fails
// leaves nothing behind.
func TestATrialIsStartedFromThePinnedProgramsAndLeavesNothing(t *testing.T) {
	bin := t.TempDir()
	calls := filepath.Join(t.TempDir(), "calls")
	starter := "#!/bin/sh\necho \"$*\" >>" + calls + "\n[ -z \"$FAIL_START\" ] || { echo refused >&2; exit 1; }\n"
	for name, body := range map[string]string{TrialStarter: starter, TrialStore: "#!/bin/sh\n", APIServer: "#!/bin/sh\n"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	trial, err := StartTrial(APIServer)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(calls)
	for _, want := range []string{"create cluster", "--components " + TrialStore + "," + APIServer,
		"--" + TrialStore + "-binary " + filepath.Join(bin, TrialStore), "--" + APIServer + "-binary " + filepath.Join(bin, APIServer),
		"--kubeconfig " + trial.Kubeconfig} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the trial was not started with %q:\n%s", want, body)
		}
	}
	if !strings.Contains(strings.Join(trial.Env, " "), "KUBECONFIG="+trial.Kubeconfig) {
		t.Errorf("a program run against the trial is not told where it is: %v", trial.Env)
	}
	trial.Stop()
	body, _ = os.ReadFile(calls)
	if !strings.Contains(string(body), "delete cluster") {
		t.Error("the trial was not stopped")
	}
	if _, err := os.Stat(trial.Dir); !os.IsNotExist(err) {
		t.Errorf("the trial's directory outlived it: %v", err)
	}

	t.Setenv("FAIL_START", "1")
	if _, err := StartTrial(APIServer); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Errorf("a starter that refused was not said, in its own words: %v", err)
	}
}

// Two starts at once can pick one port, so a start waits for the one before
// it, wherever that one is running.
func TestATrialWaitsForAStartAlreadyUnderWay(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	bin := t.TempDir()
	calls := filepath.Join(t.TempDir(), "calls")
	if err := os.WriteFile(filepath.Join(bin, TrialStarter), []byte("#!/bin/sh\necho \"$*\" >>"+calls+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	finished, err := oneStartAtATime()
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan error, 1)
	go func() {
		_, err := StartTrial(APIServer)
		started <- err
	}()
	select {
	case err := <-started:
		t.Fatalf("a trial started while another was starting: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if _, err := os.Stat(calls); !os.IsNotExist(err) {
		t.Fatal("the starter ran while another start was under way")
	}
	finished()
	select {
	case err := <-started:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a trial was still waiting after the start before it had finished")
	}
}
