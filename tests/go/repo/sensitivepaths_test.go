package repo

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The sensitive-path list is a rule, so it is checked like one.
//
// A tripwire that names a directory which no longer exists does not fail. It
// simply stops matching, and the pull request that renames the directory is
// also the pull request that silently disarms the alarm covering it. That is
// the failure this file catches, and it is the same shape as every other rule
// in this package: a check on the repository's own files, because nothing else
// in the pipeline has an opinion about it.

type sensitiveEntry struct {
	Path string
	Why  string
	Line int
}

// parseSensitivePaths reads the plain-text format: one path per line, the text
// after "#" is the reason, blank and whole-line comments ignored.
//
// Deliberately not a YAML parser. The file is read by a shell script in CI as
// well as by this test, and a format needing a dependency on both sides is a
// format that will eventually disagree with itself.
func parseSensitivePaths(body string) []sensitiveEntry {
	var out []sensitiveEntry
	for i, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		path, why, _ := strings.Cut(line, "#")
		out = append(out, sensitiveEntry{
			Path: strings.TrimSpace(path),
			Why:  strings.TrimSpace(why),
			Line: i + 1,
		})
	}
	return out
}

func loadSensitivePaths(t *testing.T) (string, []sensitiveEntry) {
	t.Helper()
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, ".github", "sensitive-paths"))
	if err != nil {
		t.Fatalf("reading .github/sensitive-paths: %v", err)
	}
	entries := parseSensitivePaths(string(body))
	if len(entries) < 5 {
		t.Fatalf("only %d paths declared; this is reading the wrong file or the list was gutted", len(entries))
	}
	return root, entries
}

// The parser is exercised on its own, because CI reimplements it in shell and
// the two must agree about what a line means.
func TestParseSensitivePaths(t *testing.T) {
	got := parseSensitivePaths(`
# a whole-line comment
   # an indented comment

path/one/     # first reason
path/two      #second reason, no space
path/three
`)
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3: %+v", len(got), got)
	}
	if got[0].Path != "path/one/" || got[0].Why != "first reason" {
		t.Errorf("entry 0 = %+v", got[0])
	}
	if got[1].Why != "second reason, no space" {
		t.Errorf("a reason with no space after # should still parse: %q", got[1].Why)
	}
	if got[2].Path != "path/three" || got[2].Why != "" {
		t.Errorf("a path with no reason should parse with an empty reason: %+v", got[2])
	}
}

func TestEverySensitivePathExists(t *testing.T) {
	root, entries := loadSensitivePaths(t)
	for _, e := range entries {
		if _, err := os.Stat(filepath.Join(root, e.Path)); err != nil {
			t.Errorf(`line %d: %s is declared sensitive but does not exist.

A tripwire pointed at a missing path does not fail - it stops matching, and
whoever renamed the path also disarmed the alarm on it without noticing.
Update this line in the same change that moved the file.`, e.Line, e.Path)
		}
	}
}

// Every entry has to say why, and say something. The reason is the whole
// payload: a banner reading "this is sensitive" is noise, while one reading
// "probe.go returns a Status and never the value it read" is a reviewer being
// told what to look for.
func TestEverySensitivePathExplainsItself(t *testing.T) {
	_, entries := loadSensitivePaths(t)
	for _, e := range entries {
		if e.Why == "" {
			t.Errorf("line %d: %s is declared sensitive with no reason given", e.Line, e.Path)
			continue
		}
		if len(e.Why) < 40 {
			t.Errorf(`line %d: %s has a reason too short to be useful: %q

Name the property that breaks, not the fact that one exists.`, e.Line, e.Path, e.Why)
		}
	}
}

// The code laws must all be covered. Adding a rule to this package without
// covering it here would leave the newest guard as the least protected one.
func TestTheCodeLawsAreCoveredBySensitivePaths(t *testing.T) {
	root, entries := loadSensitivePaths(t)

	covered := func(rel string) bool {
		for _, e := range entries {
			p := strings.TrimSuffix(e.Path, "/")
			if rel == p || strings.HasPrefix(rel, p+"/") {
				return true
			}
		}
		return false
	}

	// Discovered by reading the directory, not from a list, so a new guard
	// cannot be added without being covered.
	dir := filepath.Join(root, "tests", "go", "repo")
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	found := 0
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), "_test.go") {
			continue
		}
		found++
		if rel := filepath.Join("tests", "go", "repo", f.Name()); !covered(rel) {
			t.Errorf("%s is a code law but no sensitive path covers it", rel)
		}
	}
	if found < 3 {
		t.Fatalf("only %d guard tests found; this test is looking in the wrong place", found)
	}

	// The files existing rules name explicitly. If a rule guards a file, a
	// change to that file deserves the alarm.
	for _, rel := range []string{
		"scripts/steward/internal/onepassword/probe.go",
		"config/management.tpl.json",
		"management/cluster/registry.tf",
	} {
		if !covered(rel) {
			t.Errorf("%s is named by a code law but no sensitive path covers it", rel)
		}
	}
}

// --- the script CI actually runs -------------------------------------------

// The shell script is the thing that fires the alarm, so it is tested rather
// than trusted. Testing the Go parser alone would prove the list is well
// formed while the script that reads it could still match nothing - and a
// tripwire that matches nothing fails silently, which is the whole class of
// bug this package exists for.
func runSensor(t *testing.T, changed string) (string, error) {
	t.Helper()
	root := repoRoot(t)
	cmd := exec.Command("bash", filepath.Join(root, ".github", "scripts", "sensitive-paths.sh"))
	cmd.Dir = root
	cmd.Stdin = strings.NewReader(changed)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if err != nil {
		t.Logf("stderr: %s", errb.String())
	}
	return out.String(), err
}

func TestSensorTripsOnAGuardedPath(t *testing.T) {
	out, err := runSensor(t, "tests/go/repo/breakglass_test.go\nREADME.md\n")
	if err != nil {
		t.Fatalf("sensor failed: %v", err)
	}
	if !strings.Contains(out, "tripped=true") {
		t.Errorf("a change to a guard test must trip the alarm:\n%s", out)
	}
	// The reason is the payload - a report that names the file but not the
	// property tells a reviewer nothing they did not already see in the diff.
	if !strings.Contains(out, "green build") {
		t.Errorf("the report should carry the reason from the list:\n%s", out)
	}
}

func TestSensorIsQuietOnAnInnocuousChange(t *testing.T) {
	out, err := runSensor(t, "README.md\ndocs/ideas.md\n")
	if err != nil {
		t.Fatalf("sensor failed: %v", err)
	}
	if !strings.Contains(out, "tripped=false") {
		t.Errorf("an innocuous change must not trip the alarm:\n%s", out)
	}
}

// The match must be anchored. Unanchored, `vendor/tests/go/repo/x` trips the
// alarm for `tests/go/repo/` and every unrelated change starts crying wolf -
// which is how a tripwire becomes wallpaper and stops being read.
//
// The decoy is derived from the list rather than hardcoded. The first version
// of this test used a real path as innocuous filler, and the moment that path
// was added to the list the test failed for a reason that had nothing to do
// with anchoring.
func TestSensorDoesNotMatchAPathPrefixedElsewhere(t *testing.T) {
	_, entries := loadSensitivePaths(t)

	var decoys []string
	for _, e := range entries {
		if strings.HasSuffix(e.Path, "/") {
			decoys = append(decoys, "vendor/"+e.Path+"x.go")
		} else {
			decoys = append(decoys, "vendor/"+e.Path)
		}
	}
	if len(decoys) == 0 {
		t.Fatal("no directory entries to build a decoy from")
	}

	out, err := runSensor(t, strings.Join(decoys, "\n")+"\n")
	if err != nil {
		t.Fatalf("sensor failed: %v", err)
	}
	if !strings.Contains(out, "tripped=false") {
		t.Errorf("every declared path prefixed with vendor/ must be ignored:\n%s", out)
	}
}

// An entry without a trailing slash names one file, not a prefix.
func TestSensorFileEntryMatchesOnlyItself(t *testing.T) {
	hit, err := runSensor(t, "config/management.tpl.json\n")
	if err != nil {
		t.Fatalf("sensor failed: %v", err)
	}
	if !strings.Contains(hit, "tripped=true") {
		t.Errorf("the exact file must trip:\n%s", hit)
	}

	miss, err := runSensor(t, "config/management.tpl.json.bak\n")
	if err != nil {
		t.Fatalf("sensor failed: %v", err)
	}
	if !strings.Contains(miss, "tripped=false") {
		t.Errorf("a longer name must not trip:\n%s", miss)
	}
}

// The Go parser above and the shell script are two readers of one file. They
// have to agree about which paths exist, or the list means one thing to the
// test and another to CI.
func TestSensorAndParserAgreeOnEveryPath(t *testing.T) {
	_, entries := loadSensitivePaths(t)
	for _, e := range entries {
		probe := e.Path
		if strings.HasSuffix(probe, "/") {
			probe += "probe-file"
		}
		out, err := runSensor(t, probe+"\n")
		if err != nil {
			t.Fatalf("sensor failed for %s: %v", e.Path, err)
		}
		if !strings.Contains(out, "tripped=true") {
			t.Errorf("the parser accepts %q but the script does not match %q", e.Path, probe)
		}
	}
}
