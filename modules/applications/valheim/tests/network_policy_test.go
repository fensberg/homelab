package tests

import (
	"testing"

	"homelab/details/privatenet"
)

// The ranges players actually sit on stay open.
//
// The estate refuses any application a path into its own address space
// (tests/go/repo, "no application can be given a path into the estate"), and
// a guard that only ever refuses would be satisfied by closing everything -
// which breaks this server for every player in the house and every player on
// a hotspot. #392 is a story about a range being closed, not about one being
// open. Which ranges those are is this application's decision, so it is held
// here.
func TestTheServerCanStillReachPlayersOnOrdinaryNetworks(t *testing.T) {
	var policies []struct {
		Spec struct {
			Egress []struct {
				To []struct {
					IPBlock struct {
						CIDR string `json:"cidr"`
					} `json:"ipBlock"`
				} `json:"to"`
			} `json:"egress"`
		} `json:"spec"`
	}
	readManifest(t, "base/network-policy.yaml", &policies)
	permitted := map[string]bool{}
	for _, p := range policies {
		for _, rule := range p.Spec.Egress {
			for _, peer := range rule.To {
				permitted[peer.IPBlock.CIDR] = true
			}
		}
	}
	if len(permitted) == 0 {
		t.Fatal("base/network-policy.yaml permits egress to no address at all, so this is reading the wrong shape")
	}
	for _, want := range []struct{ cidr, who string }{
		{privatenet.OneNineTwo, "a player in the same house as the hypervisor"},
		{privatenet.OneSevenTwo, "a player on a phone hotspot, in a hotel, or on a corporate network"},
	} {
		if !permitted[want.cidr] {
			t.Errorf(`the egress policy no longer permits %s, so %s cannot be given a
direct path.

PlayFab Party offers the player's local address as a candidate, so a range that
is closed here is a player who joins, spawns, and is disconnected seconds later
with nothing anywhere naming this policy. That is #392, and it cost one remote
player every attempt they made.`, want.cidr, want.who)
		}
	}
}
