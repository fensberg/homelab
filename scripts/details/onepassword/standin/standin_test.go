package standin_test

import (
	"os"
	"strings"
	"testing"

	"homelab/details/onepassword"
	"homelab/details/onepassword/standin"
)

func TestMain(m *testing.M) {
	if status, was := standin.Answer(); was {
		os.Exit(status)
	}
	os.Exit(m.Run())
}

// Held to the real callers: what the package above runs the CLI for is what
// the stand-in has to answer, so it is asked through that package.
func TestTheStandInAnswersWhatAProgramAsksOfAVault(t *testing.T) {
	v := standin.New(t, "one", "two")
	if !onepassword.Available() || !onepassword.SignedIn() {
		t.Fatal("the stand-in is not on PATH as a CLI that is signed in")
	}
	if names, err := onepassword.Vaults(); err != nil || strings.Join(names, " ") != "one two" {
		t.Fatalf("the vaults are %v, %v", names, err)
	}

	v.Keep("one", "thing", "colour", "green")
	if got, err := onepassword.Read("op://one/thing/colour"); err != nil || got != "green" {
		t.Fatalf("read %q, %v", got, err)
	}
	if onepassword.Probe("op://one/thing/size") != onepassword.StatusMissing || onepassword.Probe("op://two/thing/colour") != onepassword.StatusMissing {
		t.Error("a field or an item the vault does not hold was not missing")
	}

	// A write makes the item, changes what differs and leaves the rest.
	changed, err := onepassword.WriteItem("two", "made", map[string]string{"a": "1", "b": "2"})
	if err != nil || strings.Join(changed, " ") != "a b" {
		t.Fatalf("a write to an item that was not there changed %v, %v", changed, err)
	}
	changed, err = onepassword.WriteItem("two", "made", map[string]string{"a": "1", "b": "3"})
	if err != nil || strings.Join(changed, " ") != "b" || v.Kept("two", "made", "a") != "1" || v.Kept("two", "made", "b") != "3" {
		t.Fatalf("a second write changed %v, %v", changed, err)
	}
	if v.Kept("two", "made", "c") != "" || v.Kept("two", "other", "a") != "" {
		t.Error("something the vault does not hold was read as held")
	}
	if _, err := onepassword.WriteItem("three", "made", map[string]string{"a": "1"}); err == nil {
		t.Error("a write to a vault that is not there was accepted")
	}
}
