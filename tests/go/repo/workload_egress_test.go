package repo

import (
	"regexp"
	"strings"
	"testing"
)

// The game server's egress policy may open private ranges, and not the one the
// estate lives in.
//
// WHY THIS IS A GUARD RATHER THAN THE PARAGRAPH IN THE MANIFEST. Which private
// ranges this policy permits is a PRODUCT decision: PlayFab Party offers a
// player's local address as a direct-path candidate, so the ranges open here
// are the networks a player may sit on. A player the policy denies joins,
// spawns, and is disconnected seconds later with nothing naming the policy -
// indistinguishable from their own connection being bad (#392).
//
// That makes "open another range" the obvious response to the next player who
// cannot connect, and 10.0.0.0/8 the obvious range, because it is the one
// remaining and it looks symmetrical with the two beside it. It is also the
// range this entire estate is addressed out of: node subnets at
// 10.<octet>.0.0/16, services at 10.96.0.0/12, pods at 10.244.0.0/16. Opening
// it hands a public-facing game server a UDP path to the API server, the state
// database, the hypervisor and every other workload - which is precisely what
// the policy exists to prevent.
//
// Nothing else would object. The manifest stays valid, kustomize builds it,
// Flux applies it, and the cluster does exactly what it was told. So the
// refusal lives here, and tests/mutations.yml proves it fires.

var ipBlockCIDR = regexp.MustCompile(`cidr:\s*(\S+)`)

// The estate's own space, and the reason it can never be a permitted peer.
const estateSpace = "10."

func TestTheGameServerCannotBeGivenAPathIntoTheEstate(t *testing.T) {
	const policy = "modules/applications/valheim/base/network-policy.yaml"
	body := readRepoFile(t, policy)

	// Everything the policy permits as a destination, ignoring the `except:`
	// entries - those are subtractions from 0.0.0.0/0 and are the opposite of
	// a grant.
	permitted, excepted := permittedPeers(body)

	if len(permitted) == 0 {
		t.Fatal("no permitted destination was found in " + policy + ", so this test " +
			"is reading the wrong shape and proves nothing")
	}

	for _, cidr := range permitted {
		if strings.HasPrefix(cidr, estateSpace) {
			t.Errorf(`the egress policy permits %s.

That is inside 10.0.0.0/8, which is the space this entire estate is addressed
out of - node subnets at 10.<octet>.0.0/16, services at 10.96.0.0/12, pods at
10.244.0.0/16.

This is a public-facing game server. Permitting it a path into that range gives
a compromised one reach to the API server, the state database, the hypervisor
and every other workload, which is the single thing this policy exists to
prevent.

If a player on a 10.x network cannot get a direct path, that is the correct
outcome and they stay on the relay. The isolation boundary wins.`, cidr)
		}
	}

	// And the broad internet rule must keep subtracting it, or the grant
	// arrives from the other direction with nothing above to notice.
	if !contains(excepted, "10.0.0.0/8") {
		t.Errorf(`the broad egress rule no longer excludes 10.0.0.0/8.

It permits 0.0.0.0/0 minus the private ranges. Dropping that exclusion opens
every address in the estate without any rule appearing to grant anything - the
change is a deleted line, and the diff is three characters shorter.

Excluded today: %s`, strings.Join(excepted, ", "))
	}
}

// The ranges players actually sit on stay open.
//
// The converse of the test above, and it needs saying: a guard that only ever
// refuses would be satisfied by closing everything, which breaks the workload
// for every player in the house and every player on a hotspot. #392 is a story
// about a range being closed, not about one being open.
func TestTheGameServerCanStillReachPlayersOnOrdinaryNetworks(t *testing.T) {
	const policy = "modules/applications/valheim/base/network-policy.yaml"
	body := readRepoFile(t, policy)
	permitted, _ := permittedPeers(body)

	for _, want := range []struct{ cidr, who string }{
		{"192.168.0.0/16", "a player in the same house as the hypervisor"},
		{"172.16.0.0/12", "a player on a phone hotspot, in a hotel, or on a corporate network"},
	} {
		if !contains(permitted, want.cidr) {
			t.Errorf(`the egress policy no longer permits %s, so %s cannot be given a
direct path.

PlayFab Party offers the player's local address as a candidate, so a range that
is closed here is a player who joins, spawns, and is disconnected seconds later
with nothing anywhere naming this policy. That is #392, and it cost one remote
player every attempt they made.`, want.cidr, want.who)
		}
	}
}

// permittedPeers splits the policy's CIDRs into what it grants and what it
// subtracts from the broad rule.
//
// Split by indentation rather than by parsing, because an `except:` entry and a
// permitted `cidr:` are the same text at different depths, and counting them
// together would make the broad internet rule look like a grant of every
// private range - which is the exact opposite of what it is.
func permittedPeers(body string) (permitted, excepted []string) {
	inExcept := false
	exceptIndent := 0

	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || trimmed == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))

		if inExcept && indent <= exceptIndent && !strings.HasPrefix(trimmed, "- ") {
			inExcept = false
		}
		if strings.HasPrefix(trimmed, "except:") {
			inExcept, exceptIndent = true, indent
			continue
		}
		if inExcept {
			if v := strings.TrimPrefix(trimmed, "- "); v != trimmed {
				excepted = append(excepted, v)
			}
			continue
		}
		if m := ipBlockCIDR.FindStringSubmatch(trimmed); m != nil && m[1] != "0.0.0.0/0" {
			permitted = append(permitted, m[1])
		}
	}
	return permitted, excepted
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
