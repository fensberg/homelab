package vaults

import (
	"strings"
	"testing"
)

func TestTheLawyerSeesTheEstateAndWhatItSharesNeverASitesOwn(t *testing.T) {
	for _, ok := range [][]string{
		{"estate"},
		{"estate", "estate-shared", "site0-shared"},
	} {
		if err := CheckLawyer(ok); err != nil {
			t.Errorf("%v was refused: %v", ok, err)
		}
	}
	if err := CheckLawyer([]string{"estate-shared", "site0-shared"}); err == nil {
		t.Error("a token that cannot see the estate vault was accepted")
	}
	err := CheckLawyer([]string{"estate", "estate-shared", "site0"})
	if err == nil {
		t.Fatal("a token reaching a site's own vault was accepted, so the lawyer could read what a site generates for itself")
	}
	if strings.Contains(err.Error(), "site0") {
		t.Errorf("the refusal names another vault, and it reaches a public log: %v", err)
	}
}

func TestASiteSeesItsOwnAndWhatTheEstateSharesNothingMore(t *testing.T) {
	for _, ok := range [][]string{
		{"site0"},
		{"site0", "site0-shared", "estate-shared"},
	} {
		if err := CheckSite("site0", ok); err != nil {
			t.Errorf("%v was refused: %v", ok, err)
		}
	}
	for name, bad := range map[string][]string{
		"no vault of its own":        {"site0-shared", "estate-shared"},
		"the estate's own vault":     {"site0", "estate"},
		"a sibling's own vault":      {"site0", "site1"},
		"a sibling's granted vault":  {"site0", "site1-shared"},
		"a vault from before scopes": {"site0", "homelab"},
	} {
		err := CheckSite("site0", bad)
		if err == nil {
			t.Errorf("%s: %v was accepted", name, bad)
			continue
		}
		for _, other := range []string{"site1", "homelab"} {
			if strings.Contains(err.Error(), other) {
				t.Errorf("%s: the refusal names another vault, and it reaches a public log: %v", name, err)
			}
		}
	}
}
