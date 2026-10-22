package parsed

import (
	"slices"
	"strings"
	"testing"
)

// Every simple command is found wherever it sits, its literal words come back
// as their values, a word that expands comes back as written, and a comment
// is no command.
func TestCommandsFindsEveryCommandAndKeepsWhatExpands(t *testing.T) {
	src := `#!/usr/bin/env bash
# git config --global commit.gpgsign true
setup() {
  git -C "$repo_root" config 'commit.gpgsign' true
}
if grep -q x file; then echo "$(date)"; fi
FOO=1 make all | tee "log.txt"
readonly LIST=(a b)
`
	f, err := ShellOf("fixture.sh", src)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range Commands(f) {
		got = append(got, strings.Join(c.Words, " "))
	}
	want := []string{
		`git -C "$repo_root" config commit.gpgsign true`,
		"grep -q x file",
		`echo "$(date)"`,
		"date",
		"make all",
		"tee log.txt",
		"readonly",
	}
	if !slices.Equal(got, want) {
		t.Errorf("Commands found\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
	for _, c := range Commands(f) {
		if len(c.Words) > 0 && c.Words[0] == "make" && c.Assigns["FOO"] != "1" {
			t.Errorf("the assignment in front of make was not kept: %v", c.Assigns)
		}
	}
	if last := Commands(f)[len(Commands(f))-1]; len(last.Assigns) != 1 || last.Assigns["LIST"] != "" {
		t.Errorf("a declared array was not kept as an assignment: %+v", last)
	}
}
