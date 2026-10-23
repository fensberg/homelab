package repo

import (
	"homelab/tests/parsed"
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

type sensitiveEntry = parsed.SensitiveEntry

func loadSensitivePaths(t *testing.T) (*parsed.Tree, []sensitiveEntry) {
	t.Helper()
	tree := repository(t)
	entries := tree.SensitivePathList(t)
	if len(entries) < 5 {
		t.Fatalf("only %d paths declared; this is reading the wrong file or the list was gutted", len(entries))
	}
	return tree, entries
}

// sensor runs the script CI runs, from the tree being judged, in a
// directory - a fixture repository the test made, or the tree itself when
// it is "" - with the changed files on its standard input.
func sensor(t *testing.T, in string, env []string, changed string) (stdout, stderr string, err error) {
	t.Helper()
	out, errs, err := repository(t).Invoke(parsed.Run{In: in, Env: env, Stdin: changed}, "bash", parsed.Here(".github/scripts/sensitive-paths.sh"))
	return string(out), string(errs), err
}

func TestEverySensitivePathExists(t *testing.T) {
	tree, entries := loadSensitivePaths(t)
	for _, e := range entries {
		if !tree.Exists(e.Path) {
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

// The building code must all be covered. Adding a rule to this package without
// covering it here would leave the newest guard as the least protected one.
func TestTheCodeLawsAreCoveredBySensitivePaths(t *testing.T) {
	tree, entries := loadSensitivePaths(t)

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
	found := 0
	for _, rel := range tree.Files(t) {
		if filepath.Dir(rel) != guardsDir || !strings.HasSuffix(rel, "_test.go") {
			continue
		}
		found++
		if !covered(rel) {
			t.Errorf("%s is a building code but no sensitive path covers it", rel)
		}
	}
	if found < 3 {
		t.Fatalf("only %d guard tests found; this test is looking in the wrong place", found)
	}

	// The files existing rules name explicitly. If a rule guards a file, a
	// change to that file deserves the alarm.
	// The file that refuses a bad config, wherever it is: found by what it
	// declares, so a rule keeps covering it when it moves.
	invariants, _ := tofuDeclaring(t, declInvariants)
	for _, rel := range []string{
		"scripts/details/onepassword/probe.go",
		"config/management.tpl.json",
		invariants,
	} {
		if !covered(rel) {
			t.Errorf("%s is named by a building code but no sensitive path covers it", rel)
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
	// Given the list as it is, which is what the parser reads, so the two
	// readers are compared on the same file.
	out, errs, err := sensor(t, "", []string{"SENSITIVE_PATHS_FILE=" + parsed.Here(parsed.SensitivePaths)}, changed)
	if err != nil {
		t.Logf("stderr: %s", errs)
	}
	return out, err
}

func TestSensorTripsOnAGuardedPath(t *testing.T) {
	out, err := runSensor(t, "tests/go/repo/vaultcheck_test.go\nREADME.md\n")
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
	heavy(t, "runs the shell sensor once per listed path, seconds")
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

// removedProofs is where a removed proof is said to have been.
const removedProofs = "tests/mutations-removed.yml"

// A proof cannot be removed from the mutation ledger without the change
// saying so.
//
// The ledger was one list with a count at its head, and a change that took
// an entry away without lowering the count failed. The count went because
// every change adding a guard raised it and so conflicted with every other.
// What it gave is kept here, by the check that already reads what a change
// touched: a proof that is gone, and that the list of removals does not name
// with a reason, stops the check. Without this, a proof that is one of two
// for its guard could be deleted and nothing would fail.
func TestAProofCannotBeRemovedWithoutSayingSo(t *testing.T) {
	site := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(site, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("list", "docs/  # nothing here is touched\n")
	write(ledgerEntries+"/StillHere.1.yml", "guard: StillHere\n")
	run := func(removals, changed string) (string, error) {
		t.Helper()
		write(removedProofs, removals)
		_, said, err := sensor(t, site, []string{"SENSITIVE_PATHS_FILE=" + filepath.Join(site, "list")}, changed)
		return said, err
	}
	const gone = ledgerEntries + "/WasProved.9.yml"

	if said, err := run("removed: []\n", ledgerEntries+"/StillHere.1.yml\nREADME.md\n"); err != nil {
		t.Errorf("a change to a proof that is still there was refused: %v\n%s", err, said)
	}
	said, err := run("removed: []\n", gone+"\n")
	if err == nil || !strings.Contains(said, gone) {
		t.Errorf("a proof was removed and the change was not refused, or what was removed was not named: %v\n%s", err, said)
	}
	if said, err := run("removed:\n  - proof: WasProved.9.yml\n    reason: the guard it proved was removed with the thing it guarded\n", gone+"\n"); err != nil {
		t.Errorf("a removal that is named with its reason was refused: %v\n%s", err, said)
	}
	if _, err := run("removed:\n  - proof: WasProved.9.yml\n    reason:\n", gone+"\n"); err == nil {
		t.Error("a removal named with no reason was let through, which is a line anybody can add")
	}
	if _, err := run("removed:\n  - proof: AnotherWasProved.9.yml\n    reason: it names a different proof\n", gone+"\n"); err == nil {
		t.Error("a removal was let through on the strength of a line naming some other proof")
	}
}

// Every removal on the list is of a proof that is really gone, with a
// reason. A line naming a proof that is still there is a removal approved
// before it has been made.
func TestARemovalNamesAProofThatIsReallyGone(t *testing.T) {
	tree := repository(t)
	var list struct {
		Removed []struct {
			Proof  string `yaml:"proof"`
			Reason string `yaml:"reason"`
		} `yaml:"removed"`
	}
	tree.YAMLInto(t, removedProofs, &list)
	for _, r := range list.Removed {
		if strings.TrimSpace(r.Reason) == "" {
			t.Errorf("%s names %q as removed and gives no reason", removedProofs, r.Proof)
		}
		if tree.Exists(ledgerEntries + "/" + r.Proof) {
			t.Errorf("%s names %q as removed, and it is still there. A removal is said in the change that makes it, not before.", removedProofs, r.Proof)
		}
	}
}

// A branch that main has moved on from is judged by what it changed, and not
// by what main gained while it was open.
//
// Between the base and the head, everything main gained since reads as
// something the branch took away. A proof main added looked removed, and
// the change was refused for it; a sensitive file main touched looked
// touched, and the conversation opened for it was anchored to a file the
// pull request did not hold, which GitHub refused on every attempt (#692).
func TestABranchBehindMainIsJudgedByItsOwnChanges(t *testing.T) {
	site := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = site
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(site, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q", "-b", "main")
	git("config", "commit.gpgsign", "false")
	git("config", "user.name", "whoever opened the piece")
	git("config", "user.email", "piece@example.invalid")
	write("list", "guarded/  # what breaks if this is wrong\n")
	write("guarded/kept", "as it was\n")
	write(removedProofs, "removed: []\n")
	write("notes", "as they were\n")
	git("add", ".")
	git("commit", "-qm", "where the branch leaves main")

	git("checkout", "-q", "-b", "piece")
	write("notes", "the piece's own change\n")
	git("commit", "-qam", "the piece")
	head := git("rev-parse", "HEAD")

	git("checkout", "-q", "main")
	write("guarded/kept", "changed on main since\n")
	write(ledgerEntries+"/AddedOnMain.1.yml", "guard: AddedOnMain\n")
	git("add", ".")
	git("commit", "-qm", "main moves on")
	base := git("rev-parse", "HEAD")
	git("checkout", "-q", "piece")

	// What the caller hands over: every file that differs between the two.
	out, said, err := sensor(t, site, []string{"SENSITIVE_PATHS_FILE=" + filepath.Join(site, "list"), "BASE=" + base, "HEAD=" + head},
		git("diff", "--name-only", base, head)+"\n")
	if err != nil {
		t.Fatalf("a branch main has moved on from was refused for what main gained: %v\n%s", err, said)
	}
	if !strings.Contains(out, "tripped=false") {
		t.Errorf("a branch that touched nothing sensitive tripped the alarm for what main touched:\n%s", out)
	}
}
