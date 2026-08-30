package repo

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// `ignite -check-vault` must never be able to disclose a secret.
//
// The whole reason it exists is to be safe to run and safe to share: it
// answers "does the vault still match the config template" and its output is
// meant to be pasteable into an issue or a pull request. That property is not
// a promise the code makes in a comment - it has to survive somebody adding a
// debug line, widening a return value, or "improving" the report to say what
// it actually found.
//
// It also cannot be enforced by the type system alone, because of a
// constraint this project already documented elsewhere: `op` has no existence
// check that does not also return the value. Probing a reference necessarily
// reads the secret. The design answer is to read it into a local, measure it,
// and drop it - onepassword.Probe returns a Status and nothing else, so a
// caller cannot print what it was never handed.
//
// These tests are what stop that design being quietly undone. They are the
// same shape, and exist for the same reason, as breakglass_test.go: an
// invariant about what the source is allowed to do, checked against the source
// as text, because nothing else in the pipeline would notice it changing.

// The files that make up the check. Named explicitly rather than discovered,
// so that moving the logic somewhere else fails loudly here instead of
// silently taking it out of scope.
var vaultCheckFiles = []string{
	"scripts/steward/internal/onepassword/probe.go",
	"scripts/steward/internal/phases/checkvault.go",
	"scripts/steward/internal/config/vaultrefs.go",
}

// Probe's signature is the load-bearing part of the design: one return value,
// of a type that cannot carry a secret. Widening it to return the value - even
// "just for an error message" - is what this guards.
var probeSignature = regexp.MustCompile(`func Probe\(ref string\) Status \{`)

func TestVaultProbeReturnsAStatusAndNothingElse(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "scripts", "steward", "internal", "onepassword", "probe.go")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading probe.go: %v", err)
	}
	if !probeSignature.Match(body) {
		t.Errorf(`onepassword.Probe no longer has the signature:

    func Probe(ref string) Status

That single return value is the guarantee, not a style choice. op cannot check
a reference's existence without returning its value, so Probe necessarily
reads the secret - and the only thing keeping it from escaping is that Probe
has no way to hand it back. A second return value, of any type that can carry
a string, removes that.

If the check genuinely needs to say more, add cases to Status. Do not return
what was read.`)
	}
}

// The calls that fetch or write a secret. None of them belongs anywhere in
// this path: the check reads the template, asks op whether each reference
// resolves, and prints statuses.
var secretBearingCalls = []string{
	"onepassword.Read(",   // returns the value
	"onepassword.Inject(", // renders the whole template to disk
	"onepassword.WriteField(",
	"onepassword.EnsureField(", // generates and stores a secret
}

func TestVaultCheckNeverFetchesOrWritesASecret(t *testing.T) {
	root := repoRoot(t)
	for _, rel := range vaultCheckFiles {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("reading %s: %v - if this file moved, move the rule with it rather than deleting it", rel, err)
		}
		if call, found := firstMatch(string(body), secretBearingCalls); found {
			t.Errorf(`%s calls %s.

The vault check must not fetch a secret's value or write one anywhere. It
exists to report structure - ok / empty / missing - and its output is meant to
be safe to paste into an issue. A call that returns or stores a value puts one
inside the blast radius of the next debug print.`, rel, call)
		}
	}
}

// Writing to a file is the other way a value escapes, and it would not look
// like a leak in review - it would look like caching, or a report someone
// wanted to keep. The check has no reason to create a file at all.
var fileWritingCalls = []string{
	"os.WriteFile(",
	"os.Create(",
	"os.OpenFile(",
	"os.Rename(",
}

func TestVaultCheckWritesNoFiles(t *testing.T) {
	root := repoRoot(t)
	for _, rel := range vaultCheckFiles {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		if call, found := firstMatch(string(body), fileWritingCalls); found {
			t.Errorf(`%s calls %s.

The vault check writes nothing. That is what makes it the one ignite mode with
nothing to sterilize afterwards - it can be interrupted at any point and leave
the workspace exactly as it found it. A file created here is a file somebody
has to remember to delete, which is the property this project already decided
not to rely on.`, rel, call)
		}
	}
}

// Guard against all three tests above passing because the feature was renamed
// or removed. Without this, deleting probe.go would turn every rule here into
// a t.Fatalf about a missing file - which is at least loud - but renaming the
// *call* while keeping the files would not be caught by anything.
func TestVaultCheckStillExists(t *testing.T) {
	root := repoRoot(t)

	probe, err := os.ReadFile(filepath.Join(root, "scripts", "steward", "internal", "phases", "checkvault.go"))
	if err != nil {
		t.Fatalf("reading checkvault.go: %v", err)
	}
	// The report has to actually be driven by a probe, or the rules above are
	// guarding a function nobody calls.
	if !strings.Contains(string(probe), "onepassword.Probe") {
		t.Fatal(`checkvault.go no longer calls onepassword.Probe.

Either the check now resolves references some other way - in which case these
rules need to point at that instead, and the new way needs the same guarantee -
or the check is gone and these tests should go with it. Passing quietly is the
one outcome that is wrong.`)
	}
}
