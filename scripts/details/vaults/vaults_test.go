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

func TestSitesAmongAreTheVaultsOfWhatASiteIsGranted(t *testing.T) {
	got := SitesAmong([]string{"site2-shared", Estate, EstateShared, "site1-shared", "site3", "-shared"})
	if len(got) != 2 || got[0] != "site1" || got[1] != "site2" {
		t.Errorf("the sites are %v; the estate's own shared vault, a site's own vault and a vault with no site in its name are not one", got)
	}
}

func TestTheNotarySeesTheOperatorsVaultAndWhatEachSiteIsGrantedNothingMore(t *testing.T) {
	if err := CheckNotary([]string{Operator, "site1-shared", "site2-shared"}); err != nil {
		t.Errorf("the vaults the notary works in were refused: %v", err)
	}
	if err := CheckNotary([]string{"site1-shared"}); err == nil || !strings.Contains(err.Error(), "cannot see") {
		t.Errorf("a token that cannot see the operator's vault was accepted: %v", err)
	}
	for _, other := range []string{Estate, EstateShared, "site1"} {
		err := CheckNotary([]string{Operator, "site1-shared", other})
		if err == nil || !strings.Contains(err.Error(), "reaches 1 vault(s)") {
			t.Errorf("a token that also reaches %s was accepted: %v", other, err)
		}
		if err != nil && strings.Contains(err.Error(), other+" ") {
			t.Errorf("the refusal names the vault it refused")
		}
	}
}
