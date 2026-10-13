package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const aPlan = `{"format_version":"1.2","resource_changes":[
  {"address":"thing.stays","mode":"managed","type":"thing","change":{"actions":["no-op"]}},
  {"address":"thing.new","mode":"managed","type":"thing","change":{"actions":["create"],"after":{"secret":"do-not-print"}}},
  {"address":"thing.rebuilt","mode":"managed","type":"thing","change":{"actions":["delete","create"]}},
  {"address":"thing.changed","mode":"managed","type":"thing","change":{"actions":["update"],"before":{"secret":"nor-this"}}},
  {"address":"thing.gone","mode":"managed","type":"thing","change":{"actions":["delete"]}},
  {"address":"data.thing.read","mode":"data","type":"thing","change":{"actions":["read"]}}
]}`

const aPlanThatChangesNothing = `{"format_version":"1.2","resource_changes":[
  {"address":"thing.stays","mode":"managed","type":"thing","change":{"actions":["no-op"]}}
]}`

// What a plan would change is said by address and by what would be done,
// and by nothing a plan holds besides: a value never reaches the line.
func TestAPlanIsSaidByAddressAndWhatWouldBeDoneAndNeverAValue(t *testing.T) {
	got, err := plannedChanges([]byte(aPlan))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"thing.changed: update", "thing.gone: delete", "thing.new: create", "thing.rebuilt: delete then create"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the changes were read as\n%s\nand are\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if said := strings.Join(got, "\n"); strings.Contains(said, "do-not-print") || strings.Contains(said, "nor-this") {
		t.Error("a value from the plan reached what is printed")
	}
	if none, err := plannedChanges([]byte(aPlanThatChangesNothing)); err != nil || len(none) != 0 {
		t.Errorf("a plan that changes nothing was read as changing %v, %v", none, err)
	}
	if _, err := plannedChanges([]byte("not a plan")); err == nil {
		t.Error("something that is not a plan was read as one that changes nothing")
	}
}

// tofuStandIn puts a program named tofu first on PATH that writes down what
// it was asked, makes the plan file a plan is asked to save, and shows the
// plan given here.
func tofuStandIn(t *testing.T, shown string) (asked func() string) {
	t.Helper()
	bin := t.TempDir()
	log := filepath.Join(bin, "asked")
	answer := filepath.Join(bin, "shown.json")
	if err := os.WriteFile(answer, []byte(shown), 0o600); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho \"$*\" >>" + log + "\ncase \"$1\" in\n" +
		"  plan) for a in \"$@\"; do case \"$a\" in -out=*) : >\"${a#-out=}\";; esac; done;;\n" +
		"  show) cat " + answer + ";;\n" +
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

// Asking what a converge would change asks tofu for a plan and to show it,
// and for nothing that changes anything; and the plan it saved, which holds
// every value, is gone when it returns.
func TestPlanningTheEstateChangesNothingAndLeavesNoPlanBehind(t *testing.T) {
	for name, shown := range map[string]string{"a plan that changes things": aPlan, "a plan that changes nothing": aPlanThatChangesNothing} {
		asked := tofuStandIn(t, shown)
		e, calls := fakeEstate(t)
		e.dir = t.TempDir()

		if err := e.plan(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		said := asked()
		if !strings.Contains(said, "plan -input=false -out=") || !strings.Contains(said, "show -json") {
			t.Errorf("%s: tofu was asked\n%s", name, said)
		}
		for _, changing := range []string{"apply", "destroy", "import", "state rm"} {
			if strings.Contains(said, changing) {
				t.Errorf("%s: tofu was asked to %s", name, changing)
			}
		}
		if len(*calls) != 0 {
			t.Errorf("%s: the lawyer's own tofu was asked %v", name, *calls)
		}
		left, _ := filepath.Glob(filepath.Join(e.dir, "*.tfplan"))
		if len(left) != 0 {
			t.Errorf("%s: a saved plan was left behind: %v", name, left)
		}
	}
}
