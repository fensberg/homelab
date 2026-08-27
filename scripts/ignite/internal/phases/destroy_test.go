package phases

import (
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
