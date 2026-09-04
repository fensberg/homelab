package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A repository built by the test, stating every input it depends on.
//
// GIT_CONFIG_GLOBAL and GIT_CONFIG_SYSTEM are neutralised because this shells
// out to git, and a developer's own config has already broken tests in this
// repository twice - once by signing a fixture that was meant to be unsigned.
// A test that reads the machine reports the machine.
func repoWith(t *testing.T, files map[string]string, untracked map[string]string) string {
	t.Helper()
	root := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, body string) {
		t.Helper()
		full := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	run("init", "-q")
	for name, body := range files {
		write(name, body)
	}
	if len(files) > 0 {
		run("add", "-A")
		run("commit", "-qm", "fixture")
	}
	for name, body := range untracked {
		write(name, body)
	}
	return root
}

// The rule the free-tier argument rests on.
//
// An untracked file in the working tree during a real run is a rendered
// config, a kubeconfig or a plan output - the exact class that must never
// reach a vendor whose terms say free-tier content improves their products.
func TestOnlyTrackedFilesAreEverRead(t *testing.T) {
	root := repoWith(t,
		map[string]string{"scripts/thing.go": "package main\n"},
		map[string]string{"site.auto.yml": "hypervisor_password: hunter2\n"},
	)

	got, err := tracked(root, []string{"."})
	if err != nil {
		t.Fatalf("tracked: %v", err)
	}
	for _, p := range got {
		if strings.Contains(p, "site.auto.yml") {
			t.Fatal("an untracked file was selected; only what git holds is already public")
		}
	}
	if len(got) != 1 || got[0] != "scripts/thing.go" {
		t.Errorf("got %v, want just the tracked file", got)
	}
}

// Asking for something untracked is an error, not an empty run.
//
// A clerk that silently reads nothing and reports no findings looks exactly
// like a clerk that read everything and found nothing wrong.
func TestAskingForOnlyUntrackedPathsFails(t *testing.T) {
	root := repoWith(t,
		map[string]string{"a.go": "package a\n"},
		map[string]string{"secret.yml": "x\n"},
	)

	if _, err := tracked(root, []string{"secret.yml"}); err == nil {
		t.Fatal("an untracked path was accepted")
	}
}

func TestReadNumbersLinesAndSeparatesTheCommentary(t *testing.T) {
	root := repoWith(t, map[string]string{
		"a.go": "package a\n\n// Retries three times.\nfunc try() {}\n",
	}, nil)

	b, err := read(root, []string{"a.go"}, 1<<20)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(b.code, "Retries three times") {
		t.Error("the claim reached the blind pass")
	}
	if !strings.Contains(b.prose, "Retries three times") {
		t.Error("the claim was lost instead of set aside")
	}
	if !strings.Contains(b.code, "4| func try() {}") {
		t.Errorf("func try() is on line 4 and was not numbered as such:\n%s", b.code)
	}
	if b.lines["a.go"] != 5 {
		t.Errorf("line count is %d, want 5; a citation is checked against it", b.lines["a.go"])
	}
}

// The budget stops before the limit rather than truncating through it.
//
// A prompt cut mid-function makes the model describe something that does not
// exist, confidently, and then cite a line for it.
func TestReadStopsAtTheBudgetRatherThanTruncating(t *testing.T) {
	big := strings.Repeat("x := 1\n", 600)
	root := repoWith(t, map[string]string{
		"a.go": "package a\n" + big,
		"b.go": "package b\n" + big,
	}, nil)

	b, err := read(root, []string{"a.go", "b.go"}, 9000)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(b.included) != 1 {
		t.Fatalf("included %v, want only the first to fit", b.included)
	}
	if strings.Contains(b.code, "package b") {
		t.Error("the second file was partly included; a half file is worse than none")
	}
}

func TestReadRefusesWhenTheFirstFileAloneIsTooBig(t *testing.T) {
	root := repoWith(t, map[string]string{"a.go": "package a\n" + strings.Repeat("y := 1\n", 600)}, nil)

	if _, err := read(root, []string{"a.go"}, 200); err == nil {
		t.Fatal("expected a refusal rather than an empty prompt")
	}
}
