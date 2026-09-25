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

// The standing order lets the expedite duty merge without a review. What keeps that
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
		return []string{"-author", "expedite[bot]", "-holder", "expedite[bot]", "-base", base, "-head", head}
	}
	if code := enforceStandingOrder(args(delivery)); code != 0 {
		t.Errorf("a digest-and-build change was refused (exit %d)", code)
	}
	if code := enforceStandingOrder(args(overreach)); code != 1 {
		t.Errorf("a change that also set runAsUser: 0 was allowed (exit %d) - that is the overreach the order exists to stop", code)
	}
	if code := enforceStandingOrder([]string{"-author", "a-person", "-holder", "expedite[bot]", "-base", base, "-head", overreach}); code != 0 {
		t.Errorf("a person's pull request was judged under the order (exit %d); people are reviewed, not held to it", code)
	}
}

// A delivery - procurement moving a release pin - passes so a person can
// merge it, and is refused under -bypass however clean it is: a delivery is
// never merged without review. Anything else in the releases file is not a
// delivery, and is refused as overreach.
func TestADeliveryWaitsForAPersonAndNeverRidesTheBypass(t *testing.T) {
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
	const releases = "clusters/management/releases.yaml"
	write := func(tag string, d byte, extra string) {
		if err := os.MkdirAll(filepath.Dir(releases), 0o755); err != nil {
			t.Fatal(err)
		}
		body := "kind: OCIRepository\nmetadata:\n  name: valheim\nspec:\n  interval: 1m\n" + extra +
			"  ref:\n    tag: \"" + tag + "\"\n    digest: \"sha256:" + digest(d) + "\"\n"
		if err := os.WriteFile(releases, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	git("init", "-q")
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

	args := func(head string, extra ...string) []string {
		return append([]string{"-author", "procurement[bot]", "-holder", "procurement[bot]", "-base", base, "-head", head, "-releases", releases}, extra...)
	}
	if code := enforceStandingOrder(args(delivery)); code != 0 {
		t.Errorf("a clean delivery was refused on the pull request (exit %d), so a person could never merge it", code)
	}
	if code := enforceStandingOrder(args(delivery, "-bypass")); code != 1 {
		t.Errorf("a delivery passed under -bypass (exit %d); it would be merged without review", code)
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
