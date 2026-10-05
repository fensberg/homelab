package onepassword

import (
	"slices"
	"testing"
)

func TestVaultNamesReadsEveryVaultOpListed(t *testing.T) {
	got, err := vaultNames([]byte(`[{"id":"a1","name":"example"},{"id":"b2","name":"other"}]`))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"example", "other"}) {
		t.Fatalf("got %v", got)
	}
	if _, err := vaultNames([]byte(`not json`)); err == nil {
		t.Fatal("an unreadable answer was accepted as a list of vaults")
	}
}

// A value is put into the JSON config exactly as the vault holds it. One with
// a line break, a quote or a backslash stops the config parsing, and that is
// told apart from a value that is merely there.
func TestAValueTheConfigCannotCarryIsNotReportedAsUsable(t *testing.T) {
	for read, want := range map[string]Status{
		"a-value\n":                StatusOK,
		"":                         StatusEmpty,
		"   \n":                    StatusEmpty,
		"first line\nsecond\n":     StatusBreaksConfig,
		"carriage\rreturn\n":       StatusBreaksConfig,
		"a \"quoted\" word\n":      StatusBreaksConfig,
		"a back\\slash\n":          StatusBreaksConfig,
		"YmFzZTY0IG9mIGEga2V5\n":   StatusOK,
		"https://example.invalid/": StatusOK,
	} {
		if got := statusOf(read); got != want {
			t.Errorf("%q read as %s, want %s", read, got, want)
		}
	}
}
