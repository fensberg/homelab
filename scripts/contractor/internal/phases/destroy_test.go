package phases

import (
	"os"
	"strings"
	"testing"
)

// ConfirmDestroy is the only thing standing between a typo and a destroyed
// estate, so it is worth more tests than it has lines. The rule it enforces
// is deliberately awkward: naming the site twice, once to select it and once
// to confirm it, is not something that happens by accident.

func TestConfirmDestroy_MatchingNamesPass(t *testing.T) {
	if err := ConfirmDestroy("site0", "site0"); err != nil {
		t.Errorf("unexpected error for matching names: %v", err)
	}
}

func TestConfirmDestroy_EmptyConfirmationIsRefused(t *testing.T) {
	err := ConfirmDestroy("site0", "")
	if err == nil {
		t.Fatal("expected an error when -confirm is absent")
	}
	// The message has to show the exact command, or the natural next move is
	// to go looking for a flag that skips the check.
	if !strings.Contains(err.Error(), "-confirm site0") {
		t.Errorf("the error should show the exact flag to pass, got: %v", err)
	}
}

func TestConfirmDestroy_MismatchIsRefused(t *testing.T) {
	err := ConfirmDestroy("site0", "site1")
	if err == nil {
		t.Fatal("expected an error when -confirm names a different site")
	}
	// Both names must appear: the whole point of this failure is that the
	// operator is looking at one site and thinking about another.
	for _, want := range []string{"site0", "site1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should name both sites, %q is missing from: %v", want, err)
		}
	}
}

// Not case-insensitive, not whitespace-trimmed, not a prefix match. Every
// loosening here is a way for a confirmation to succeed that the operator did
// not actually type.
func TestConfirmDestroy_IsExact(t *testing.T) {
	for _, confirm := range []string{"SITE0", "Site0", " site0", "site0 ", "site", "site00"} {
		if err := ConfirmDestroy("site0", confirm); err == nil {
			t.Errorf("ConfirmDestroy(\"site0\", %q) passed; the match must be exact", confirm)
		}
	}
}

// A site named "" would make an empty -confirm succeed, which would turn the
// guard off entirely for whoever managed to reach that state.
func TestConfirmDestroy_EmptySiteIsRefusedEvenWhenConfirmMatches(t *testing.T) {
	if err := ConfirmDestroy("", ""); err == nil {
		t.Fatal("expected an error for an empty site name; an empty confirmation must never satisfy the guard")
	}
}

// The teardown works from state; the banner used to be built from the rendered
// config. When the two disagree the banner under-reports, and it under-reports
// in the reassuring direction at the moment somebody is deciding whether to
// proceed with something irreversible (#93).
//
// Parsing is tested here rather than the printing, because the parsing is the
// part that can be wrong in a way nobody sees: a pattern that matches nothing
// reports zero machines, which reads exactly like an estate that is already
// gone.
func TestMachinesInStateReadsTheControlPlaneInstances(t *testing.T) {
	const out = `data.talos_cluster_health.this
proxmox_virtual_environment_download_file.talos_image["node0"]
proxmox_virtual_environment_vm.talos_template["node0"]
proxmox_virtual_environment_vm.talos_cp["node2"]
proxmox_virtual_environment_vm.talos_cp["node0"]
proxmox_virtual_environment_vm.talos_cp["node1"]
talos_machine_secrets.this
tailscale_tailnet_key.hypervisor`

	got := machinesInState(out)
	want := []string{"node0", "node1", "node2"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v (sorted)", got, want)
		}
	}
}

// The template is a VM too, and counting it would overstate by one on every
// estate. Overstating is the safer direction but it is still wrong, and a
// banner nobody trusts is a banner nobody reads.
func TestMachinesInStateIgnoresTheTemplateAndEverythingElse(t *testing.T) {
	const out = `proxmox_virtual_environment_vm.talos_template["node0"]
proxmox_virtual_environment_file.cloud_init["node0"]
module.something.proxmox_virtual_environment_vm.talos_cp["node9"]`

	if got := machinesInState(out); len(got) != 0 {
		t.Errorf("got %v, want none - the template, an unrelated resource and a "+
			"module-nested address are all not this cluster's control plane", got)
	}
}

func TestMachinesInStateOnEmptyStateReportsNone(t *testing.T) {
	if got := machinesInState(""); len(got) != 0 {
		t.Errorf("got %v from empty output, want none", got)
	}
}

// The teardown says what it will remove, and asks where there is a human.
//
// -confirm is not consent to a scope: it runs before Render, refuses only a
// mismatch between two flags, and shows nothing. That makes it a guard against
// a typo by somebody who already holds the credentials - a real property, and a
// different one from being shown what will go (#213).
//
// The irreversible line in that list is the object storage. The machines are
// disposable and come back from a build-site; the age-encrypted state dumps
// do not, and the teardown empties the bucket because Cloudflare will not
// delete a non-empty one (#94).
func TestTheTeardownNamesTheObjectStorageBeforeEmptyingIt(t *testing.T) {
	src, err := os.ReadFile("destroy.go")
	if err != nil {
		t.Fatalf("reading destroy.go: %v", err)
	}
	body := string(src)

	// The scope must be confirmed BEFORE the teardown, not reported after it.
	// Ordering is the whole property and it is the half "the call exists"
	// cannot see.
	confirm := strings.Index(body, "confirmDestroyScope(ctx)")
	teardown := strings.Index(body, "res := tearDown(ctx)")
	if confirm < 0 {
		t.Fatal("Destroy no longer confirms the scope, so a teardown shows nothing " +
			"and takes the state backups with it")
	}
	if teardown < 0 {
		t.Fatal("Destroy no longer calls tearDown, so this test is reading something " +
			"other than the teardown path")
	}
	if confirm > teardown {
		t.Error("the scope is confirmed after tearDown has run, which is a receipt " +
			"rather than consent - by then the bucket is empty")
	}
}

// A non-terminal run is not prompted, and is not silent either.
//
// The e2e tier tears down the estate it just built, with nobody there to
// answer. Blocking on a prompt would hang it; skipping the whole report would
// lose the one record of what went. So the plan prints either way and only the
// question is conditional.
//
// This is deliberately not a `-yes` flag. Such a flag exists to be passed
// habitually, by a human, on the one path where the question is worth asking -
// which is how a guard ends up switched off.
func TestTheScopeIsPrintedEvenWithNobodyToAsk(t *testing.T) {
	src, err := os.ReadFile("destroy.go")
	if err != nil {
		t.Fatalf("reading destroy.go: %v", err)
	}
	body := string(src)

	if !strings.Contains(body, "stdinIsATerminal()") {
		t.Fatal("the prompt is no longer conditional on there being a human to ask, " +
			"so an unattended teardown either hangs or was given a flag that skips it")
	}

	// The report has to happen before the terminal check, or a non-interactive
	// run records nothing.
	report := strings.Index(body, "reportObjectStorageAtRisk(ctx)")
	check := strings.Index(body, "if !stdinIsATerminal()")
	if report < 0 {
		t.Fatal("nothing reports the object storage, so the one irreversible part of " +
			"a teardown is the part nobody is told about")
	}
	if check >= 0 && report > check {
		t.Error("the object storage is reported after the terminal check, so an " +
			"unattended teardown empties the state backups with no record of what " +
			"was in the bucket")
	}

	for _, flag := range []string{`"yes"`, `"force"`, `"no-prompt"`} {
		if strings.Contains(body, "fs.Bool("+flag) {
			t.Errorf("destroy.go has grown a %s flag. A flag that skips the scope "+
				"prompt is one a human passes habitually, which is the guard switched "+
				"off rather than satisfied.", flag)
		}
	}
}

// There is a human to ask only when stdin is a terminal.
//
// This is the whole of what decides between prompting and recording, so it is
// worth the four lines: getting it backwards either hangs the e2e tier on a
// question nobody can answer, or silently drops the prompt on the one path
// where it is wanted.
func TestAPipeIsNotAHumanToAsk(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()

	saved := os.Stdin
	defer func() { os.Stdin = saved }()

	os.Stdin = r
	if stdinIsATerminal() {
		t.Error("a pipe was read as a terminal, so an unattended teardown would block " +
			"on a prompt nobody can answer - which is how the e2e tier hangs")
	}

	// A character device is what a terminal is. /dev/null is one and is
	// available everywhere this runs, so the positive case is testable without
	// allocating a pty.
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Skipf("no %s to test the positive case with: %v", os.DevNull, err)
	}
	defer func() { _ = devNull.Close() }()
	os.Stdin = devNull
	if !stdinIsATerminal() {
		t.Error("a character device was not read as a terminal, so the prompt would " +
			"never fire and a teardown would never ask anybody anything")
	}
}
