package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const pinFile = "scripts/versions.env"

func digest(c byte) string { return strings.Repeat(string(c), 64) }

// The standing order lets the expediter merge without a review. What keeps that
// safe is that it covers one specified item and nothing else - one Steam build
// replaced by another in the pins file - so these cases are the whole of the
// permission, written down.
func TestAStandingOrderCoversTheSteamBuildAndNothingElse(t *testing.T) {
	oldBuild, newBuild := "-VALHEIM_STEAM_BUILD_VERSION=20001", "+VALHEIM_STEAM_BUILD_VERSION=20002"
	for _, tc := range []struct {
		name    string
		files   []string
		changed []string
		wantOK  bool
		wantSay string
	}{
		{name: "one build replaced by the next", files: []string{pinFile}, changed: []string{oldBuild, newBuild}, wantOK: true},
		{name: "any other file", files: []string{pinFile, "modules/applications/valheim/image/Dockerfile"},
			changed: []string{oldBuild, newBuild}, wantSay: "Dockerfile"},
		{name: "any other pin in the file", files: []string{pinFile},
			changed: []string{oldBuild, newBuild, "-OTHER_TOOL_VERSION=1.0", "+OTHER_TOOL_VERSION=9.9"}, wantSay: "OTHER_TOOL_VERSION"},
		{name: "a build that is not a number", files: []string{pinFile},
			changed: []string{oldBuild, "+VALHEIM_STEAM_BUILD_VERSION=$(curl evil)"}, wantSay: "does not cover"},
		{name: "a build added and none removed", files: []string{pinFile}, changed: []string{newBuild}, wantSay: "exactly one"},
		{name: "nothing at all", wantSay: "changes nothing"},
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

// A throwaway repository with a committer set, for tests that need real
// commits to diff.
func gitRepo(t *testing.T) func(args ...string) string {
	t.Helper()
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
	git("init", "-q")
	return git
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// End to end, against a real repository: the verb diffs the two commits itself,
// so the git wiring is exercised and not only the rule.
func TestTheVerbJudgesARealPullRequest(t *testing.T) {
	git := gitRepo(t)
	pins := func(build, rclone string) string {
		return "OTHER_TOOL_VERSION=" + rclone + "\nVALHEIM_STEAM_BUILD_VERSION=" + build + "\n"
	}
	writeFile(t, pinFile, pins("20001", "1.0"))
	git("add", "-A")
	git("commit", "-qm", "base")
	base := git("rev-parse", "HEAD")

	writeFile(t, pinFile, pins("20002", "1.0"))
	git("commit", "-qam", "a new Steam build")
	delivery := git("rev-parse", "HEAD")

	writeFile(t, pinFile, pins("20003", "9.9"))
	git("commit", "-qam", "a new Steam build, and another pin")
	overreach := git("rev-parse", "HEAD")

	args := func(head string) []string {
		return []string{"-author", "procurement[bot]", "-holder", "procurement[bot]", "-base", base, "-head", head}
	}
	if code := enforceStandingOrder(args(delivery)); code != 0 {
		t.Errorf("a Steam build change was refused (exit %d)", code)
	}
	if code := enforceStandingOrder(args(overreach)); code != 1 {
		t.Errorf("a change that also moved another pin was allowed (exit %d) - that is the overreach the order exists to stop", code)
	}
	if code := enforceStandingOrder([]string{"-author", "a-person", "-holder", "procurement[bot]", "-base", base, "-head", overreach}); code != 0 {
		t.Errorf("a person's pull request was judged under the order (exit %d); people are reviewed, not held to it", code)
	}
}

// A delivery passes on the pull request, so a person can merge it; a pin move
// that also changes anything else in the releases file is not a delivery.
func TestADeliveryPassesForAPersonAndNothingRidesAlongWithIt(t *testing.T) {
	git := gitRepo(t)
	const releases = "clusters/management/releases.yaml"
	write := func(tag string, d byte, extra string) {
		writeFile(t, releases, "kind: OCIRepository\nmetadata:\n  name: valheim\nspec:\n  interval: 1m\n"+extra+
			"  ref:\n    tag: \""+tag+"\"\n    digest: \"sha256:"+digest(d)+"\"\n")
	}
	write("1.0.15-5", 'a', "")
	git("add", "-A")
	git("commit", "-qm", "base")
	base := git("rev-parse", "HEAD")

	write("1.0.15-6", 'b', "")
	git("commit", "-qam", "a delivery")
	delivery := git("rev-parse", "HEAD")

	write("1.0.15-6", 'b', "  suspend: true\n")
	git("commit", "-qam", "a delivery that also suspends production")
	overreach := git("rev-parse", "HEAD")

	args := func(head string) []string {
		return []string{"-author", "procurement[bot]", "-holder", "procurement[bot]", "-base", base, "-head", head, "-releases", releases}
	}
	if code := enforceStandingOrder(args(delivery)); code != 0 {
		t.Errorf("a clean delivery was refused on the pull request (exit %d), so a person could never merge it", code)
	}
	if code := enforceStandingOrder(args(overreach)); code != 1 {
		t.Errorf("a pin move that also suspended production passed (exit %d); only the pin may change", code)
	}
}

func TestIsDeliveryAcceptsOnlyPinLines(t *testing.T) {
	const f = "clusters/management/releases.yaml"
	tag := `-    tag: "1.0.15-5"`
	newTag := `+    tag: "1.0.15-6"`
	dig := `+    digest: "sha256:` + digest('c') + `"`
	for name, tc := range map[string]struct {
		files, lines []string
		want         bool
	}{
		"a pin move":                  {[]string{f}, []string{tag, newTag, dig}, true},
		"another file too":            {[]string{f, "x.yaml"}, []string{newTag}, false},
		"latest instead of a release": {[]string{f}, []string{`+    tag: "latest"`}, false},
		"a line that is not a pin":    {[]string{f}, []string{newTag, `+  suspend: true`}, false},
		"an empty change":             {[]string{f}, nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := isDelivery(tc.files, tc.lines, f); got != tc.want {
				t.Errorf("isDelivery = %v, want %v", got, tc.want)
			}
		})
	}
}
