package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"homelab/details/repopath"
)

// standInScanner puts a trufflehog on PATH that writes down what it was asked
// and exits with the given status, and nothing else on PATH.
func standInScanner(t *testing.T, status int) string {
	t.Helper()
	dir := t.TempDir()
	asked := filepath.Join(dir, "asked")
	script := "#!/bin/sh\necho \"$*\" > " + asked + "\nexit " + strconv.Itoa(status) + "\n"
	if err := os.WriteFile(filepath.Join(dir, secretsScanner), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return asked
}

func TestGuardSecretsScansEveryCommitBetweenTheTwoAndVerifies(t *testing.T) {
	top, err := repopath.Here()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		annotate bool
		want     string
	}{
		{false, "git file://" + top + " --since-commit origin/main --branch HEAD --fail --no-update --results=verified,unknown"},
		{true, "git file://" + top + " --since-commit origin/main --branch HEAD --fail --no-update --results=verified,unknown --github-actions"},
	} {
		asked := standInScanner(t, 0)
		var out, errs bytes.Buffer
		if code := guardSecretsTo([]string{"-base", "origin/main", "-head", "HEAD"}, c.annotate, &out, &errs); code != 0 {
			t.Fatalf("a clean scan answered %d: %s", code, errs.String())
		}
		got, err := os.ReadFile(asked)
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(got)) != c.want {
			t.Errorf("the scan was asked\n  %s\nwant\n  %s", strings.TrimSpace(string(got)), c.want)
		}
	}
}

func TestGuardSecretsRefusesWhatTheScanFound(t *testing.T) {
	standInScanner(t, secretsFound)
	var out, errs bytes.Buffer
	if code := guardSecretsTo([]string{"-base", "b", "-head", "h"}, false, &out, &errs); code != 1 {
		t.Errorf("a scan that found something answered %d", code)
	}
	if !strings.Contains(errs.String(), "is in the commits after b") || !strings.Contains(errs.String(), "%s where the value goes") {
		t.Errorf("a finding said:\n%s", errs.String())
	}
}

// Not knowing is not a clean answer: a scan that broke, or no scanner.
func TestGuardSecretsRefusesAScanThatDidNotHappen(t *testing.T) {
	standInScanner(t, 2)
	var out, errs bytes.Buffer
	if code := guardSecretsTo([]string{"-base", "b", "-head", "h"}, false, &out, &errs); code != 1 || !strings.Contains(errs.String(), "stopped short") {
		t.Errorf("a scan that broke answered %d: %s", code, errs.String())
	}

	t.Setenv("PATH", t.TempDir())
	errs.Reset()
	if code := guardSecretsTo([]string{"-base", "b", "-head", "h"}, false, &out, &errs); code != 1 || !strings.Contains(errs.String(), "take-delivery.sh trufflehog") {
		t.Errorf("no scanner answered %d: %s", code, errs.String())
	}

	t.Chdir(t.TempDir())
	errs.Reset()
	if code := guardSecretsTo([]string{"-base", "b", "-head", "h"}, false, &out, &errs); code != 1 || errs.Len() == 0 {
		t.Errorf("outside a checkout answered %d: %s", code, errs.String())
	}
}

func TestGuardSecretsNeedsTwoCommits(t *testing.T) {
	var out, errs bytes.Buffer
	for _, args := range [][]string{{}, {"-base", "b"}, {"-head", "h"}, {"-nonsense"}} {
		if code := guardSecretsTo(args, false, &out, &errs); code != 2 {
			t.Errorf("%q answered %d", args, code)
		}
	}
	if code := guardSecrets(nil); code != 2 {
		t.Errorf("the verb with no commits answered %d", code)
	}
}
