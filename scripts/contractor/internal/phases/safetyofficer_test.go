package phases

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homelab/contractor/internal/run"
)

// officer puts a `go` on PATH that records what it was asked to run and
// exits as told, and returns what it recorded.
func officer(t *testing.T, exit string) func() string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "asked")
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\nexit " + exit + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() string {
		b, _ := os.ReadFile(log)
		return string(b)
	}
}

func TestTheSafetyOfficerIsAskedAboutThisSiteAndNothingElse(t *testing.T) {
	asked := officer(t, "0")
	ctx := run.NewContext(t.TempDir(), "site7")
	if err := clearedBySafetyOfficer(ctx); err != nil {
		t.Fatalf("an officer that cleared the teardown was read as refusing it: %v", err)
	}
	want := "run -C " + filepath.Join(ctx.RepoRoot, "scripts", "safety-officer") + " . clear -site site7 -destroying site"
	if got := strings.TrimSpace(asked()); got != want {
		t.Errorf("the officer was asked\n  %s\nand should have been asked\n  %s\nwith nothing after it: an argument more is an order it would be taking", got, want)
	}
}

func TestATeardownTheSafetyOfficerDoesNotClearDoesNotStart(t *testing.T) {
	officer(t, "1")
	err := clearedBySafetyOfficer(run.NewContext(t.TempDir(), "site0"))
	if err == nil {
		t.Fatal("the officer refused and the teardown was allowed to go on")
	}
	if !strings.Contains(err.Error(), "Nothing has been destroyed") {
		t.Errorf("the refusal does not say that nothing was destroyed: %v", err)
	}
}
