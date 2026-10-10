package main

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	"homelab/details/applications"
	"homelab/details/files"
)

// An application as the tests declare one: kept on its supplier's build,
// which its own pins file records on the line it names.
const (
	thingDeclaration = `{"release": {"version": {"env": [], "pattern": "v\\([0-9.]*\\)", "example": {"line": "thing v1.2", "version": "1.2"}}}, "upstream": {"kind": "steam", "app": "10", "news": "20", "pin": "THING_BUILD_VERSION"}}`
	quietDeclaration = `{}`
)

var (
	thingRoot   = applications.Dir + "/thing"
	pinFile     = thingRoot + "/" + applications.Pins
	declaration = thingRoot + "/" + applications.Declaration
	estatePins  = "scripts/versions.env"
)

func declaredForTests(t *testing.T) map[string]applications.Application {
	t.Helper()
	out := map[string]applications.Application{}
	for name, body := range map[string]string{"thing": thingDeclaration, "quiet": quietDeclaration} {
		a, err := applications.Parse(name, []byte(body))
		if err != nil {
			t.Fatal(err)
		}
		out[name] = a
	}
	return out
}

func digest(c byte) string { return strings.Repeat(string(c), 64) }

// The standing order lets the expediter merge without a review. What keeps that
// safe is that it covers one specified item and nothing else - one build
// replaced by another, on the line an application declares, in that
// application's own pins file - so these cases are the whole of the
// permission, written down.
func TestAStandingOrderCoversTheDeclaredPinAndNothingElse(t *testing.T) {
	oldBuild, newBuild := "-THING_BUILD_VERSION=20001", "+THING_BUILD_VERSION=20002"
	quietPins := applications.Dir + "/quiet/" + applications.Pins
	for _, tc := range []struct {
		name    string
		files   []string
		changed []string
		wantOK  bool
		wantSay string
	}{
		{name: "one build replaced by the next", files: []string{pinFile}, changed: []string{oldBuild, newBuild}, wantOK: true},
		{name: "any other file", files: []string{pinFile, thingRoot + "/image/Dockerfile"},
			changed: []string{oldBuild, newBuild}, wantSay: "Dockerfile"},
		{name: "any other pin in the file", files: []string{pinFile},
			changed: []string{oldBuild, newBuild, "-OTHER_TOOL_VERSION=1.0", "+OTHER_TOOL_VERSION=9.9"}, wantSay: "OTHER_TOOL_VERSION"},
		{name: "a build that is not a number", files: []string{pinFile},
			changed: []string{oldBuild, "+THING_BUILD_VERSION=$(curl evil)"}, wantSay: "does not cover"},
		{name: "a number with something after it", files: []string{pinFile},
			changed: []string{oldBuild, "+THING_BUILD_VERSION=20002 # and more"}, wantSay: "does not cover"},
		{name: "a build added and none removed", files: []string{pinFile}, changed: []string{newBuild}, wantSay: "exactly one"},
		{name: "the estate's own pins", files: []string{estatePins}, changed: []string{oldBuild, newBuild}, wantSay: estatePins},
		{name: "the pins of an application with no upstream", files: []string{quietPins}, changed: []string{oldBuild, newBuild}, wantSay: "no pins file of an application that declares an upstream"},
		{name: "the declaration that says which line", files: []string{pinFile, declaration}, changed: []string{oldBuild, newBuild}, wantSay: declaration},
		{name: "a pin whose name only starts the same", files: []string{pinFile},
			changed: []string{"-THING_BUILD_VERSION_TOO=1", "+THING_BUILD_VERSION_TOO=2"}, wantSay: "does not cover"},
		{name: "nothing at all", wantSay: "changes nothing"},
	} {
		problems := withinStandingOrder(tc.files, tc.changed, declaredForTests(t))
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
	if err := files.Write(path, []byte(body)); err != nil {
		t.Fatal(err)
	}
}

// End to end, against a real repository: the verb diffs the two commits itself
// and reads the declarations at the base, so the git wiring is exercised and
// not only the rule.
func TestTheVerbJudgesARealPullRequest(t *testing.T) {
	git := gitRepo(t)
	pins := func(build, other string) string {
		return "OTHER_TOOL_VERSION=" + other + "\nTHING_BUILD_VERSION=" + build + "\n"
	}
	writeFile(t, declaration, thingDeclaration)
	writeFile(t, pinFile, pins("20001", "1.0"))
	writeFile(t, estatePins, "GO_VERSION=1.0\n")
	git("add", "-A")
	git("commit", "-qm", "base")
	base := git("rev-parse", "HEAD")

	writeFile(t, pinFile, pins("20002", "1.0"))
	git("commit", "-qam", "a new build")
	delivery := git("rev-parse", "HEAD")

	writeFile(t, pinFile, pins("20003", "9.9"))
	git("commit", "-qam", "a new build, and another pin")
	overreach := git("rev-parse", "HEAD")

	// A pull request that renames the pin in the declaration and moves the
	// line it now names: judged by the declaration the base had, it changes
	// a line the order does not cover, and the declaration besides.
	git("reset", "-q", "--hard", base)
	writeFile(t, declaration, strings.ReplaceAll(thingDeclaration, "THING_BUILD_VERSION", "OTHER_TOOL_VERSION"))
	writeFile(t, pinFile, pins("20001", "2"))
	git("commit", "-qam", "redeclare the pin, then move it")
	redeclared := git("rev-parse", "HEAD")

	args := func(head string) []string {
		return []string{"-author", "procurement[bot]", "-holder", "procurement[bot]", "-base", base, "-head", head}
	}
	if code := enforceStandingOrder(args(delivery)); code != 0 {
		t.Errorf("a change to the declared pin alone was refused (exit %d)", code)
	}
	if code := enforceStandingOrder(args(overreach)); code != 1 {
		t.Errorf("a change that also moved another pin was allowed (exit %d) - that is the overreach the order exists to stop", code)
	}
	if code := enforceStandingOrder(args(redeclared)); code != 1 {
		t.Errorf("a pull request that rewrote which line the order covers was allowed (exit %d)", code)
	}
	if code := enforceStandingOrder([]string{"-author", "a-person", "-holder", "procurement[bot]", "-base", base, "-head", overreach}); code != 0 {
		t.Errorf("a person's pull request was judged under the order (exit %d); people are reviewed, not held to it", code)
	}
	if code := enforceStandingOrder([]string{"-author", "procurement[bot]", "-holder", "", "-base", base, "-head", overreach}); code != 0 {
		t.Errorf("with no standing order configured the verb exited %d; it judges nothing then", code)
	}
	if code := enforceStandingOrder([]string{"-author", "procurement[bot]", "-holder", "procurement[bot]"}); code != 2 {
		t.Errorf("with no commits to judge the verb exited %d, want 2", code)
	}
}

// A base whose declarations cannot be read is not evidence that the change is
// covered: the verb refuses to judge rather than passing it.
func TestAnUnreadableDeclarationIsNotAPass(t *testing.T) {
	git := gitRepo(t)
	writeFile(t, declaration, `{"unread": true}`)
	writeFile(t, pinFile, "THING_BUILD_VERSION=1\n")
	git("add", "-A")
	git("commit", "-qm", "base")
	base := git("rev-parse", "HEAD")
	writeFile(t, pinFile, "THING_BUILD_VERSION=2\n")
	git("commit", "-qam", "a new build")
	head := git("rev-parse", "HEAD")
	if code := enforceStandingOrder([]string{"-author", "procurement[bot]", "-holder", "procurement[bot]", "-base", base, "-head", head}); code != 2 {
		t.Errorf("exited %d, want 2", code)
	}
}

// A delivery passes on the pull request, so a person can merge it; a pin move
// that also changes anything else in the site's file is not a delivery.
func TestADeliveryPassesForAPersonAndNothingRidesAlongWithIt(t *testing.T) {
	git := gitRepo(t)
	site := applications.SiteFilePath("north")
	write := func(tag string, d byte, extra string) {
		writeFile(t, site, "kind: OCIRepository\nmetadata:\n  name: thing\nspec:\n  interval: 1m\n"+extra+
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
	git("commit", "-qam", "a delivery that also suspends the application")
	overreach := git("rev-parse", "HEAD")

	args := func(head string) []string {
		return []string{"-author", "procurement[bot]", "-holder", "procurement[bot]", "-base", base, "-head", head}
	}
	if code := enforceStandingOrder(args(delivery)); code != 0 {
		t.Errorf("a clean delivery was refused on the pull request (exit %d), so a person could never merge it", code)
	}
	if code := enforceStandingOrder(args(overreach)); code != 1 {
		t.Errorf("a pin move that also suspended the application passed (exit %d); only the pin may change", code)
	}
}

func TestIsDeliveryAcceptsOnlyPinLines(t *testing.T) {
	pattern := applications.SitesDir + "/*/" + applications.SiteFile
	f, other := applications.SiteFilePath("north"), applications.SiteFilePath("south")
	was := releasesFile('a')
	moved := strings.Replace(strings.Replace(was, `"1.0.16-1"`, `"1.0.16-2"`, 1), digest('a'), digest('c'), 1)
	if moved == was || !strings.Contains(moved, digest('c')) {
		t.Fatal("the fixture did not move its pin, so nothing below is tested")
	}
	for name, tc := range map[string]struct {
		files []string
		is    string
		want  bool
	}{
		"a pin move":                         {[]string{f}, moved, true},
		"the same release to two sites":      {[]string{f, other}, moved, true},
		"another site's file":                {[]string{other}, moved, true},
		"another file too":                   {[]string{f, "x.yaml"}, moved, false},
		"latest instead of a release":        {[]string{f}, strings.Replace(moved, `"1.0.16-2"`, `"latest"`, 1), false},
		"a tag with no digest moved":         {[]string{f}, strings.Replace(was, `"1.0.16-1"`, `"1.0.16-2"`, 1), true},
		"a digest that is not one":           {[]string{f}, strings.Replace(moved, digest('c'), "not-a-digest", 1), false},
		"a line that is not a pin":           {[]string{f}, moved + "---\nkind: Kustomization\nmetadata:\n  name: thing\nspec:\n  suspend: true\n", false},
		"a comment that rode along":          {[]string{f}, "# while I was here\n" + moved, false},
		"a pin written another way":          {[]string{f}, strings.Replace(moved, `"1.0.16-2"`, `1.0.16-2`, 1), false},
		"a release source renamed":           {[]string{f}, strings.Replace(moved, "name: thing", "name: other", 1), false},
		"an empty change":                    {[]string{f}, was, false},
		"no file at all":                     {nil, moved, false},
		"a file of that name elsewhere":      {[]string{"elsewhere/" + applications.SiteFile}, moved, false},
		"a file of that name a level down":   {[]string{applications.SitesDir + "/north/extra/" + applications.SiteFile}, moved, false},
		"another file in a site's directory": {[]string{applications.SitesDir + "/north/kustomization.yaml"}, moved, false},
	} {
		t.Run(name, func(t *testing.T) {
			texts := func(string) (string, string, error) { return was, tc.is, nil }
			if got := isDelivery(tc.files, pattern, texts); got != tc.want {
				t.Errorf("isDelivery = %v, want %v", got, tc.want)
			}
		})
	}
	// A file that cannot be read at either end is not a delivery.
	if isDelivery([]string{f}, pattern, func(string) (string, string, error) { return "", "", errors.New("the commit is not there") }) {
		t.Error("a file that could not be read was taken for a delivery")
	}
}
