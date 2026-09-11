package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const pinFile = "modules/applications/valheim/base/deployment.yaml"

func digest(c byte) string { return strings.Repeat(string(c), 64) }

// The standing order lets the expediter merge without a review. What keeps that
// safe is that it covers one specified item and nothing else - the game
// server's image digest and the Steam build it records - so these cases are the
// whole of the permission, written down.
func TestAStandingOrderCoversThePinAndNothingElse(t *testing.T) {
	oldImg := "-          image: ghcr.io/example/homelab-valheim@sha256:" + digest('a')
	newImg := "+          image: ghcr.io/example/homelab-valheim@sha256:" + digest('b')
	oldBuild := `-    homelab.example.com/steam-build: "20001"`
	newBuild := `+    homelab.example.com/steam-build: "20002"`

	for _, tc := range []struct {
		name    string
		files   []string
		changed []string
		wantOK  bool
		wantSay string
	}{
		{
			name:  "a new digest and the build it came from",
			files: []string{pinFile}, changed: []string{oldImg, newImg, oldBuild, newBuild},
			wantOK: true,
		},
		{
			name:  "any other file",
			files: []string{pinFile, "clusters/management/flux-system/gotk-sync.yaml"}, changed: []string{oldImg, newImg},
			wantSay: "gotk-sync.yaml",
		},
		{
			name:  "any other line in the pin file",
			files: []string{pinFile}, changed: []string{oldImg, newImg, "+            runAsUser: 0"},
			wantSay: "runAsUser: 0",
		},
		{
			// The case the standing order most needs to refuse, because it reads
			// exactly like a routine update in a diff: same line, new digest - of
			// somebody else's image.
			name:  "a digest from a different image repository",
			files: []string{pinFile},
			changed: []string{oldImg,
				"+          image: ghcr.io/somebody-else/homelab-valheim@sha256:" + digest('b')},
			wantSay: "different image repository",
		},
		{
			name:    "nothing at all",
			files:   nil,
			changed: nil,
			wantSay: "changes nothing",
		},
	} {
		problems := withinStandingOrder(tc.files, tc.changed, pinFile)
		if ok := len(problems) == 0; ok != tc.wantOK {
			t.Errorf("%s: within=%v, want %v (problems: %v)", tc.name, ok, tc.wantOK, problems)
			continue
		}
		if tc.wantSay != "" && !strings.Contains(strings.Join(problems, "\n"), tc.wantSay) {
			t.Errorf("%s: refused, but did not name %q: %v", tc.name, tc.wantSay, problems)
		}
	}
}

// Diff context and file headers are not changes; only added and removed lines
// are, and those are what the order is judged on.
func TestOnlyAddedAndRemovedLinesAreChanges(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/" + pinFile + " b/" + pinFile,
		"--- a/" + pinFile,
		"+++ b/" + pinFile,
		"@@ -1 +1 @@",
		"-old",
		"+new",
		" context",
	}, "\n")
	got := strings.Join(changedLines(diff), "|")
	if got != "-old|+new" {
		t.Errorf("got %q, want %q", got, "-old|+new")
	}
}

// End to end, against a real repository: the verb diffs the two commits itself,
// so the git wiring is exercised and not only the rule.
func TestTheVerbJudgesARealPullRequest(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Chdir(t.TempDir())
	git := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-c", "user.email=a@example.com", "-c", "user.name=t"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(body string) {
		if err := os.MkdirAll(filepath.Dir(pinFile), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(pinFile, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manifest := func(d byte, build, user string) string {
		return "metadata:\n  annotations:\n    homelab.example.com/steam-build: \"" + build + "\"\n" +
			"spec:\n  template:\n    spec:\n      containers:\n        - name: valheim\n" +
			"          image: ghcr.io/example/homelab-valheim@sha256:" + digest(d) + "\n" +
			"          securityContext:\n            runAsUser: " + user + "\n"
	}

	git("init", "-q")
	write(manifest('a', "20001", "10000"))
	git("add", "-A")
	git("commit", "-qm", "base")
	base := git("rev-parse", "HEAD")

	write(manifest('b', "20002", "10000"))
	git("commit", "-qam", "a delivery")
	delivery := git("rev-parse", "HEAD")

	write(manifest('c', "20003", "0"))
	git("commit", "-qam", "a delivery that also runs as root")
	overreach := git("rev-parse", "HEAD")

	args := func(head string) []string {
		return []string{"-author", "expediter[bot]", "-expediter", "expediter[bot]", "-base", base, "-head", head}
	}
	if code := guardStandingOrder(args(delivery)); code != 0 {
		t.Errorf("a digest-and-build change was refused (exit %d)", code)
	}
	if code := guardStandingOrder(args(overreach)); code != 1 {
		t.Errorf("a change that also set runAsUser: 0 was allowed (exit %d) - that is the overreach the order exists to stop", code)
	}
	if code := guardStandingOrder([]string{"-author", "a-person", "-expediter", "expediter[bot]", "-base", base, "-head", overreach}); code != 0 {
		t.Errorf("a person's pull request was judged under the order (exit %d); people are reviewed, not held to it", code)
	}
}
