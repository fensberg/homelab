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
