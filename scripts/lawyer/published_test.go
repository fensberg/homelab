package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tofuThatTalks is a tofu on PATH that prints a value on both streams, makes
// the plan file it is asked to save, shows the plan given, and fails at the
// subcommand named, if one is.
func tofuThatTalks(t *testing.T, shown, failsAt string) (asked func() string) {
	t.Helper()
	bin := t.TempDir()
	log := filepath.Join(bin, "asked")
	answer := filepath.Join(bin, "shown.json")
	if err := os.WriteFile(answer, []byte(shown), 0o600); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho \"$*\" >>" + log + "\n" +
		"echo a-value-tofu-printed >&2\n" +
		"[ \"$1\" = \"" + failsAt + "\" ] && exit 1\n" +
		"case \"$1\" in\n" +
		"  plan) for a in \"$@\"; do case \"$a\" in -out=*) : >\"${a#-out=}\";; esac; done;;\n" +
		"  show) cat " + answer + ";;\n" +
		"  *) echo a-value-tofu-printed;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(bin, "tofu"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() string {
		b, _ := os.ReadFile(log)
		return string(b)
	}
}

// Where the log is public the estate is planned, and that plan and no other
// is applied: never an apply that plans again for itself. The plan it saved
// is gone afterwards.
func TestWhereTheLogIsPublicTheEstateAppliesThePlanItSaid(t *testing.T) {
	asked := tofuThatTalks(t, aPlan, "")
	e := &estate{cfg: validConfig(), dir: t.TempDir(), published: true}
	e.run = e.tofuUnseen

	if err := e.apply(); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(asked()), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "plan -input=false -out=") || !strings.HasPrefix(lines[1], "show -json ") {
		t.Fatalf("tofu was asked\n%s", asked())
	}
	saved := strings.TrimPrefix(lines[0], "plan -input=false -out=")
	if lines[2] != "apply -input=false "+saved {
		t.Errorf("the apply was %q, and is to be of the plan that was said, %s", lines[2], saved)
	}
	if strings.Contains(asked(), "-auto-approve") {
		t.Error("an apply was asked to plan for itself")
	}
	if left, _ := filepath.Glob(filepath.Join(e.dir, "*.tfplan")); len(left) != 0 {
		t.Errorf("a saved plan was left behind: %v", left)
	}
}

// A failure where the log is public says which step failed and that tofu's
// words are withheld, and carries none of them - whichever step it was.
func TestAFailureWhereTheLogIsPublicCarriesNothingTofuPrinted(t *testing.T) {
	for _, step := range []string{"plan", "show", "apply"} {
		tofuThatTalks(t, aPlan, step)
		e := &estate{cfg: validConfig(), dir: t.TempDir(), published: true}
		e.run = e.tofuUnseen

		err := e.apply()
		if err == nil {
			t.Fatalf("a failing %s was not a failure", step)
		}
		if !strings.Contains(err.Error(), "tofu "+step) || !strings.Contains(err.Error(), "withheld") {
			t.Errorf("%s: the failure says %q", step, err)
		}
		if strings.Contains(err.Error(), "a-value-tofu-printed") {
			t.Errorf("%s: what tofu printed reached the failure: %q", step, err)
		}
	}
}

// On a workstation the apply is tofu's own, and a failure there carries
// what tofu said: the one person reading it needs it.
func TestOnAWorkstationTheApplyIsTofusOwnAndAFailureSaysWhy(t *testing.T) {
	e, calls := fakeEstate(t)
	if err := e.apply(); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || strings.Join((*calls)[0], " ") != "apply -input=false -auto-approve" {
		t.Errorf("tofu was asked %v", *calls)
	}

	tofuThatTalks(t, aPlan, "plan")
	e = &estate{cfg: validConfig(), dir: t.TempDir()}
	if _, err := e.planTo(filepath.Join(e.dir, "x.tfplan")); err == nil || !strings.Contains(err.Error(), "a-value-tofu-printed") {
		t.Errorf("a failure on a workstation did not carry what tofu said: %v", err)
	}
}
