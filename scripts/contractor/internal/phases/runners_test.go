package phases

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"homelab/details/platform"
)

// scriptTofu puts a tofu first on PATH whose behaviour is the body given,
// run by sh with the arguments tofu was called with.
func scriptTofu(t *testing.T, body string) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "tofu"), []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// The state goes into the copy through stdin, never a file, with the lock
// skipped because a copy has nobody else to hold it; a refusal says why.
func TestStateIsPushedThroughStdin(t *testing.T) {
	scriptTofu(t, "echo \"$*\" > args\ncat > received\n")
	dir := t.TempDir()
	if err := pushState(dir, []byte(`{"serial": 4}`)); err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args"))
	got, _ := os.ReadFile(filepath.Join(dir, "received"))
	if strings.TrimSpace(string(args)) != "state push -lock=false -" || string(got) != `{"serial": 4}` {
		t.Errorf("pushed %q with %q", got, args)
	}

	scriptTofu(t, "echo 'Error: Failed to write state' >&2\nexit 1\n")
	if err := pushState(dir, []byte("{}")); err == nil || !strings.Contains(err.Error(), "Failed to write state") {
		t.Errorf("a refused push reported %v", err)
	}
}

// The phases run the real runner: a record that cannot be taken is a
// failure, except in a converge; a plan that cannot start says why; and each
// removes its workspace however it ends.
func TestThePhasesRunTofuAndCleanUpAfterIt(t *testing.T) {
	scriptTofu(t, "echo 'Error: the estate is unreachable' >&2\nexit 1\n")

	ctx := recordContext(t)
	for _, d := range []string{ctx.AsBuiltDir, ctx.Dir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := Record(ctx); err == nil || !strings.Contains(err.Error(), "unreachable") {
		t.Errorf("a record against an unreachable estate: %v", err)
	}
	if _, err := os.Stat(ctx.AsBuiltDir); err == nil {
		t.Error("the record left its workspace behind")
	}
	ctx.Converge = true
	if err := Record(ctx); err != nil {
		t.Errorf("a failed record failed the converge: %v", err)
	}

	ctx = recordContext(t)
	if err := os.MkdirAll(ctx.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Plan(ctx); err == nil || !strings.Contains(err.Error(), "unreachable") {
		t.Errorf("a plan that could not start: %v", err)
	}
	if _, err := os.Stat(ctx.AsBuiltDir); err == nil {
		t.Error("the plan left its copy behind")
	}
	t.Setenv(tokenVariables[0], "a-token")
	mustWriteFile(t, filepath.Join(ctx.RepoRoot, filepath.FromSlash(platform.VersionsFile)), `{"`+ctx.Site+`": {"platform": "v2026.10.1@sha256:`+strings.Repeat("ab", 32)+`"}}`)
	if err := os.MkdirAll(filepath.Dir(ctx.RegistryCredential), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := PlanAsBuilt(ctx, t.TempDir()); err == nil || !strings.Contains(err.Error(), "record") {
		t.Errorf("an empty directory was planned against as a record: %v", err)
	}
	if _, err := os.Stat(ctx.RegistryCredential); err == nil {
		t.Error("the plan left the registry credential behind")
	}
	if got, set := os.LookupEnv(platform.CLIConfigVariable); set {
		t.Errorf("the plan left tofu pointed at %q", got)
	}
}
