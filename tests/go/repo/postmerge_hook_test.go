package repo

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The post-merge hook must never cost the pull anything, and must say so when
// it could not do its job.
//
// WHAT THIS GUARDS. The collector ran by hand only, and on 2026-09-11 an
// operator's checkout still held branches from pull requests merged two weeks
// earlier - the collector recognised every one, but nothing ran it. The hook
// runs it after each pull. The failure worth guarding is the quiet one: a hook
// that cannot build the collector and exits without a word looks exactly like
// a hook with nothing to collect, and that is the silent absence that filled
// the branch picker in the first place.
//
// Run for real rather than read, in a repository where the collector cannot be
// built, which is the path where silence would do the damage.
func TestThePostMergeHookSaysWhenItCouldNotCollect(t *testing.T) {
	root := repoRoot(t)
	dir := t.TempDir()
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + dir,
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	}

	initRepo := exec.Command("git", "init", "-q", dir)
	initRepo.Env = env
	if out, err := initRepo.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	// The path is spelled out on the line that runs it, so the coverage check
	// can see this file is executed rather than merely read.
	cmd := exec.Command("bash", root+"/githooks/post-merge")
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()

	if err != nil {
		t.Errorf("the hook exited with %v; git ignores this exit status, but a failing hook "+
			"still prints as an error after every pull, which teaches people to ignore it\n%s", err, out)
	}
	if !strings.Contains(string(out), "were not taken away") {
		t.Errorf(`the hook could not build the collector and said nothing about it:

%s
A silent failure here reads exactly like "nothing to collect", which is how
finished branches piled up unnoticed before this hook existed.`, out)
	}
}
